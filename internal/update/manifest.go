package update

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

var checksumPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var commitPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

var assetLimits = map[string]int64{
	"panel-linux-amd64.tar.gz": maxBinary,
	"panel-linux-arm64.tar.gz": maxBinary,
	"install.sh":               256 << 10,
	"update.sh":                16 << 10,
	"release.json":             32 << 10,
	"SHA256SUMS":               16 << 10,
}

type archiveRecord struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type binaryRecord struct {
	OS      string        `json:"os"`
	Arch    string        `json:"arch"`
	Name    string        `json:"name"`
	Size    int64         `json:"size"`
	SHA256  string        `json:"sha256"`
	Archive archiveRecord `json:"archive"`
}

type manifest struct {
	Format     int            `json:"format"`
	Version    string         `json:"version"`
	Tag        string         `json:"tag"`
	Repository string         `json:"repository"`
	DataSchema int            `json:"dataSchema"`
	Commit     string         `json:"commit"`
	BuildTime  string         `json:"buildTime"`
	Binaries   []binaryRecord `json:"binaries"`
}

func validateAssets(repo string, release githubRelease) error {
	if len(release.Assets) > 32 {
		return errors.New("Release 产物数量超过安全上限")
	}
	seen := make(map[string]bool, len(assetLimits))
	for _, asset := range release.Assets {
		limit, required := assetLimits[asset.Name]
		if !required {
			continue
		}
		if seen[asset.Name] || asset.URL != canonicalAsset(repo, release.Tag, asset.Name) || asset.Size <= 0 || asset.Size > limit {
			return errors.New("Release 产物重复、大小异常或不是固定 GitHub 下载地址")
		}
		seen[asset.Name] = true
	}
	if len(seen) != len(assetLimits) {
		return errors.New("缺少 Linux 程序、安装脚本、版本清单或 SHA256SUMS")
	}
	return nil
}

func validateManifest(data, checksumData []byte, repo, version string, schema int, release githubRelease) error {
	sums, err := parseChecksums(checksumData)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != sums["release.json"] {
		return errors.New("release.json 与 SHA256SUMS 不一致")
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return errors.New("版本清单存在重复字段或结构异常")
	}
	var m manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil || m.Format != 2 || m.Version != version || m.Tag != release.Tag || m.Repository != repo || !commitPattern.MatchString(m.Commit) {
		return errors.New("版本清单与仓库、标签或提交信息不匹配")
	}
	if m.DataSchema != schema || schema != 1 {
		return errors.New("数据格式不同，需专门迁移流程；普通脚本升级已禁用")
	}
	buildTime, err := time.Parse("2006-01-02T15:04:05Z", m.BuildTime)
	if err != nil || buildTime.Format("2006-01-02T15:04:05Z") != m.BuildTime {
		return errors.New("构建时间格式无效")
	}
	if len(m.Binaries) != 2 {
		return errors.New("版本清单必须包含 amd64 和 arm64 两种 Linux 程序")
	}
	seen := make(map[string]bool, 2)
	for _, binary := range m.Binaries {
		if binary.OS != "linux" || binary.Arch != "amd64" && binary.Arch != "arm64" || binary.Name != "panel" || binary.Size <= 0 || binary.Size > maxBinary || !checksumPattern.MatchString(binary.SHA256) || seen[binary.Arch] {
			return errors.New("包内程序的平台、文件名、大小或摘要不符合发行约定")
		}
		archive := binary.Archive
		if archive.Name != "panel-linux-"+binary.Arch+".tar.gz" || archive.Size <= 0 || archive.Size > maxBinary || !checksumPattern.MatchString(archive.SHA256) {
			return errors.New("压缩包名称、大小或摘要不符合发行约定")
		}
		if sums[archive.Name] != archive.SHA256 {
			return errors.New("压缩包摘要与 SHA256SUMS 不一致")
		}
		for _, asset := range release.Assets {
			if asset.Name == archive.Name && asset.Size != archive.Size {
				return errors.New("压缩包大小与 Release 资源记录不一致")
			}
		}
		seen[binary.Arch] = true
	}
	return nil
}

func parseChecksums(data []byte) (map[string]string, error) {
	result := make(map[string]string, 5)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || !checksumPattern.MatchString(fields[0]) {
			return nil, errors.New("SHA256SUMS 格式无效")
		}
		name := fields[1]
		_, allowed := assetLimits[name]
		if !allowed || name == "SHA256SUMS" || result[name] != "" {
			return nil, errors.New("校验清单包含未知文件或重复记录")
		}
		result[name] = fields[0]
	}
	if len(result) != 5 {
		return nil, errors.New("SHA256SUMS 缺少必要文件")
	}
	return result, nil
}

// Metadata is small; token validation prevents duplicate fields and deep nesting.
func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 8 {
			return errors.New("too deeply nested")
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, compound := token.(json.Delim)
		if !compound {
			return nil
		}
		switch delim {
		case '{':
			keys := map[string]bool{}
			for decoder.More() {
				token, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := token.(string)
				lower := strings.ToLower(key)
				if !ok || keys[lower] {
					return errors.New("duplicate JSON key")
				}
				keys[lower] = true
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected delimiter %s", delim)
		}
		_, err = decoder.Token()
		return err
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
