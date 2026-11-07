# 维护与发布

仓库：[LinyuFiee/QQON-Panel](https://github.com/LinyuFiee/QQON-Panel) · 文档：[GitHub Pages](https://linyufiee.github.io/QQON-Panel/)

## 当前发行：v0.5.0

- [v0.5.0 正式 Release](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.5.0) 已发布，正式 tag 指向 `07e3665b582ce68198704fa38180e15f135167d6`。
- [发布 CI #42328127375](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42328127375) 的 `test-build`、`windows-test`、`publish` 均为 `success`。
- 九个附件重新下载后通过 `verify-only`；Windows bootstrap 只读 `Get-QPReleaseMetadata` 检查 v0.5.0 通过，本地面板版本检查为 `state=up_to_date`、`assetsReady=true`。

这些是发布、产物与只读检查证据，不是逐系统安装或真实 SCM 验收；APT/DNF、SELinux、真实服务权限和物理故障恢复仍需专用测试环境。

## 文档与程序分开发布

- `main` 上的文档和一键引导变更触发 `.github/workflows/docs.yml`，只发布静态站点。
- 程序 Release 由稳定标签 `vMAJOR.MINOR.PATCH` 触发 `.github/workflows/release.yml`。
- 文档可以先于 Release 更新，但必须显著标记尚未发布的能力。不要把没有发行文件的一键入口写成已经可安装。
- 当前已发布 v0.5.0 使用 format3、多平台九资产；旧版 v0.4.0 保留 format2、Linux 六资产。不要替换或移动已经公开的旧版本。

## 发版前检查

```sh
go test ./...
go vet ./...
python3 -I -m unittest discover -s scripts/tests
python3 -I -m unittest discover -s tools/tests
npm --prefix docs ci
npm --prefix docs run build
```

Windows 还需原生 PowerShell 隔离测试：

```powershell
powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File scripts/windows-tests/test_install.ps1
```

执行策略仅对测试进程生效，不修改系统全局策略。真实服务注册、管理员 ACL、APT/DNF、SELinux、Windows Server 版本和物理故障恢复仍需专门测试环境，不能只看跨编译成功。

检查 `git diff --cached` 和文件清单；不上传私有账户、Token、初始化令牌、TOTP、测试凭据、机器信息或 `.zcode`。

## 创建发行

仅在另行确认下一版本、提交与变更说明后，设置实际稳定版本号 `VERSION` 再创建新标签。已发布的 `v0.5.0` 不要重打或移动：

```sh
git tag -a "$VERSION" -m "QQON-Panel $VERSION"
git push origin "$VERSION"
```

Actions 将验证标签身份，运行 Linux 测试／vet／race、Windows Go 和 PowerShell 测试；构建自己的产物并安全解压验证 build-info。所有测试通过后，发布任务才创建草稿、上传完整产物、复核再公开。

## format3 九个文件

| 文件 | 内容 |
| --- | --- |
| `panel-linux-amd64.tar.gz` | 唯一 USTAR 普通文件 `panel` |
| `panel-linux-arm64.tar.gz` | 同上，ARM64 |
| `panel-windows-amd64.zip` | 唯一普通文件 `panel.exe`，原生 x64 PE |
| `install.sh` / `update.sh` | Linux 管理脚本与包装入口 |
| `install.ps1` / `update.ps1` | Windows 管理脚本与包装入口 |
| `release.json` | format3、三个平台记录、压缩包和程序各自的大小／摘要 |
| `SHA256SUMS` | 其他八个文件的摘要 |

用户数据格式仍为1，与发行清单格式不同。Windows 与 Linux 程序／安装器不互换；新读取器可以解释旧 format2 版本状态，但不能让 Windows 安装旧 Linux 产物。

发布脚本固定资产集、严格输入、有限解压，只在新建输出目录中生成文件。发布任务不执行从 artifact 下载的程序。

## 本地打包

需要 Go 1.26+、Python 3，使用实际完整 commit：

```sh
python3 -I tools/build_release.py \
  --version v0.5.0 --repo LinyuFiee/QQON-Panel \
  --commit FULL_LOWERCASE_COMMIT_SHA \
  --build-time 2026-11-07T00:00:00Z \
  --output /absolute/existing-parent/new-release-directory
```

仅校验已有发行目录：

```sh
python3 -I tools/build_release.py --verify-only \
  --version v0.5.0 --repo LinyuFiee/QQON-Panel \
  --commit FULL_LOWERCASE_COMMIT_SHA \
  --output /absolute/release-directory
```

开发机器上的合成 fixture 必须标为 test-only，不能当真实发行上传。`panel -build-info` 不创建账号或数据目录。

## 旧管理器迁移

v0.4.0 的更新器不认识新九资产协议。新交互安装器只允许已核实官方旧管理器，经终端单独确认后备份并迁移管理器；账户数据不变，未完成的事务先恢复。未知手动安装／改写过的管理器拒绝接管，不能通过改摘要、删锁或重置账户来绕过。

迁移成功后应用升级是单独操作，失败可恢复旧管理器和安装记录。详见文档的升级与迁移页。

## 信任与失败

- 一行安装命令信任 Pages、GitHub 仓库及发布权限；SHA-256 不是独立发布签名。
- 不运行整机升级，不禁用 TLS 或 SELinux 来“修复”安装。
- 构建／上传失败只留下未发布草稿或失败任务，不把半成品设为 Latest。
- 旧 Release 不覆盖，新修复发布新版本；数据迁移须另行设计，不能假称二进制回退恢复任意数据。
- 文档构建依赖审计信息见 `docs/guide/docs-development.md`，不公开 Vite 开发服务器；Pages 只部署静态产物。
