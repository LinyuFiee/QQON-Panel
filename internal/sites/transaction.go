package sites

import (
	"context"
	"errors"
	"net/url"
	"os"
	"time"

	"panel/internal/apps"
	"panel/internal/jobrunner"
)

func (s *Service) prepareRecord(ctx context.Context, next *record, info apps.RuntimeInfo) error {
	in, err := s.validateInput(inputFromSite(next.Site))
	if err != nil {
		return err
	}
	next.Site.Name, next.Site.Domains, next.Site.Type, next.Site.Port, next.Site.Enabled = in.Name, in.Domains, in.Type, in.Port, in.Enabled
	next.Site.Upstream, next.Site.PHPRuntime = in.Upstream, in.PHPRuntime
	next.PHPSocket, next.ProxyCA = "", ""
	if in.Type == "php" {
		for _, php := range info.PHPRuntimes {
			if php.ID == in.PHPRuntime && php.Unit == php.ID+".service" && knownPHPSocket(php.ID, php.Socket) {
				next.PHPSocket = php.Socket
				break
			}
		}
		if next.PHPSocket == "" {
			return failure(409, "php_runtime_unavailable", "所选 PHP-FPM 运行时未启动或没有已验证的本机 socket")
		}
	}
	if in.Type == "proxy" {
		u, _ := url.Parse(in.Upstream)
		if u.Scheme == "https" {
			if s.adapters.caBundle == nil {
				return errUnavailable
			}
			next.ProxyCA, err = s.adapters.caBundle()
			if err != nil || !knownCA(next.ProxyCA) {
				return failure(503, "proxy_trust_unavailable", "HTTPS 代理需要受支持的本机 CA 证书包")
			}
		}
	}
	if next.Site.TLSEnabled {
		if next.Site.Port == 80 {
			return failure(400, "tls_port", "TLS 不能绑定 HTTP 默认端口 80；请选择 443 或非面板的高位端口")
		}
		if err := s.checkCertificate(*next); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	next.Token, err = randomID()
	if err != nil {
		return err
	}
	s.setDerived(next)
	config, err := s.render(*next)
	if err != nil {
		return err
	}
	next.ConfigHash = digest(config)
	return s.conflict(*next)
}

func (s *Service) mutate(ctx context.Context, before, after *record, info apps.RuntimeInfo, p *jobrunner.Progress) (err error) {
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.verifyState(); err != nil {
		return err
	}
	id := ""
	if before != nil {
		id = before.Site.ID
	}
	if after != nil {
		id = after.Site.ID
	}
	if !idPattern.MatchString(id) {
		return errUnsafe
	}
	config, err := absoluteRoot(s.adapters.configRoot, false, 0755, false, s.adapters.strict)
	if err != nil {
		return err
	}
	defer config.Close()
	name := "qqon-" + id + ".conf"
	actual, err := currentHash(config, name, maxConfigBytes, s.adapters.strict)
	if err != nil {
		return err
	}
	expected := ""
	if before != nil {
		expected = before.ConfigHash
	}
	if actual != expected {
		return errConflict
	}
	if before != nil {
		root, err := s.ownedWebroot(*before)
		if err != nil {
			return err
		}
		root.Close()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	tx := transaction{Format: 1, ID: id, Before: before, After: after}
	journalHash, err := s.saveJournal(tx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		// A cancelled request/runner context must not prevent a bounded rollback.
		// Permission is nevertheless checked again: revocation freezes the journal.
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		if rollbackErr := s.rollback(rollbackCtx, tx, journalHash, info.NginxBinary, p); rollbackErr != nil {
			s.mu.Lock()
			s.blocked = "网站事务回滚未完成，写入已冻结；修复依赖、受管配置或授权后重启以恢复"
			s.mu.Unlock()
			err = failure(503, "rollback_pending", "操作失败且回滚尚未完成；已保留中断恢复记录并冻结后续写入，网站文件未删除")
		}
	}()
	if before == nil {
		updateProgress(p, "webroot", 15, "创建独立受管网站目录与初始测试内容")
		if err := s.createWebroot(*after); err != nil {
			return err
		}
	}
	if err := s.permission(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	updateProgress(p, "configuration", 35, "原子替换当前网站的受管配置")
	if after == nil {
		err = removeOwned(config, name, expected, maxConfigBytes, s.adapters.strict)
	} else {
		var data []byte
		data, err = s.render(*after)
		if err == nil {
			err = atomicFile(config, name, data, 0644, expected, maxConfigBytes, s.adapters.strict)
		}
	}
	if err != nil {
		return err
	}
	updateProgress(p, "nginx_test", 50, "测试完整 Nginx 配置（不写入其它网站配置）")
	if err := s.runCommand(ctx, "test", info.NginxBinary); err != nil {
		return err
	}
	updateProgress(p, "nginx_reload", 65, "重载 Nginx 服务")
	if err := s.runCommand(ctx, "reload", info.NginxBinary); err != nil {
		return err
	}
	if after != nil {
		updateProgress(p, "http_verify", 80, "通过固定本机连接验证网站标识与生效状态")
		if err := s.adapters.verify(ctx, *after, before == nil); err != nil {
			return err
		}
	} else if before != nil {
		if err := s.adapters.verifyRemoved(ctx, *before); err != nil {
			return err
		}
	}
	if err := s.permission(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.RLock()
	next := copyRecords(s.records)
	s.mu.RUnlock()
	if after == nil {
		delete(next, id)
	} else {
		next[id] = *after
	}
	if err := s.persist(next); err != nil {
		return err
	}
	if err := s.clearJournal(journalHash); err != nil {
		return err
	}
	committed = true
	updateProgress(p, "verified", 100, "网站配置已验证并保存；删除操作始终保留网站文件")
	return nil
}

func (s *Service) create(ctx context.Context, id string, in siteInput, p *jobrunner.Progress) error {
	info, err := s.runtimeInfo(ctx)
	if err != nil {
		return err
	}
	s.mu.RLock()
	_, exists := s.records[id]
	s.mu.RUnlock()
	if exists {
		return errConflict
	}
	now := time.Now().UTC()
	next := record{Site: Site{ID: id, Name: in.Name, Domains: in.Domains, Type: in.Type, Port: in.Port, Enabled: in.Enabled, Upstream: in.Upstream, PHPRuntime: in.PHPRuntime, Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err := s.prepareRecord(ctx, &next, info); err != nil {
		return err
	}
	return s.mutate(ctx, nil, &next, info, p)
}

func (s *Service) patch(ctx context.Context, id string, revision uint64, in siteInput, p *jobrunner.Progress) error {
	old, err := s.revision(id, revision)
	if err != nil {
		return err
	}
	info, err := s.runtimeInfo(ctx)
	if err != nil {
		return err
	}
	next := old
	next.Site = publicSite(old)
	next.Site.Name, next.Site.Domains, next.Site.Type, next.Site.Port, next.Site.Enabled = in.Name, in.Domains, in.Type, in.Port, in.Enabled
	next.Site.Upstream, next.Site.PHPRuntime = in.Upstream, in.PHPRuntime
	next.Site.Revision++
	if next.Site.Revision == 0 {
		return errConflict
	}
	next.Site.UpdatedAt = time.Now().UTC()
	if err := s.prepareRecord(ctx, &next, info); err != nil {
		return err
	}
	return s.mutate(ctx, &old, &next, info, p)
}

func (s *Service) delete(ctx context.Context, id string, revision uint64, p *jobrunner.Progress) error {
	old, err := s.revision(id, revision)
	if err != nil {
		return err
	}
	info, err := s.runtimeInfo(ctx)
	if err != nil {
		return err
	}
	return s.mutate(ctx, &old, nil, info, p)
}

func (s *Service) importTLS(ctx context.Context, id string, revision uint64, cert, key []byte, p *jobrunner.Progress) error {
	old, err := s.revision(id, revision)
	if err != nil {
		return err
	}
	if old.Site.Port == 80 {
		return failure(400, "tls_port", "导入证书前请将网站端口改为 443 或非面板的高位端口")
	}
	info, err := s.runtimeInfo(ctx)
	if err != nil {
		return err
	}
	next := old
	next.Site = publicSite(old)
	next.Site.TLSEnabled = true
	next.Site.Revision++
	if next.Site.Revision == 0 {
		return errConflict
	}
	next.Site.UpdatedAt = time.Now().UTC()
	if err := s.storeCertificate(&next, cert, key); err != nil {
		return err
	}
	if err := s.prepareRecord(ctx, &next, info); err != nil {
		return err
	}
	return s.mutate(ctx, &old, &next, info, p)
}

// A missing config is not implicitly adopted, regenerated or overwritten by GET.
func (s *Service) configStatus(r record) string {
	root, err := absoluteRoot(s.adapters.configRoot, false, 0755, false, s.adapters.strict)
	if err != nil {
		return "unavailable"
	}
	defer root.Close()
	hash, err := currentHash(root, "qqon-"+r.Site.ID+".conf", maxConfigBytes, s.adapters.strict)
	if errors.Is(err, os.ErrNotExist) || hash == "" && err == nil {
		return "missing"
	}
	if err != nil || hash != r.ConfigHash {
		return "conflict"
	}
	return r.Site.ConfigStatus
}
