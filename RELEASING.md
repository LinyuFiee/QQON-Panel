# 维护与发布

仓库：[LinyuFiee/QQON-Panel](https://github.com/LinyuFiee/QQON-Panel) · 文档：[GitHub Pages](https://linyufiee.github.io/QQON-Panel/)

<a id="当前发行v050"></a>

## 当前发行：v0.6.0

- 当前稳定版／`latest` 为 [v0.6.0 正式 Release](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.6.0)，正式 tag 指向 `3cfd1a8fe75eca46fd1d991749c1be39f546638c`。
- [发布 CI #42349315659](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42349315659) 的 `test-build`、`windows-test`、`publish` 均为 `success`。
- Windows 新增 maintenance 隔离测试已在本次 CI 通过，不是宿主真实服务安装测试。
- 面板更新检查显示当前 v0.6.0、`state=up_to_date`、`assetsReady=true`；Windows bootstrap 真实 format3 三平台元数据检查通过，Pages 两个 bootstrap HTTPS 200 且摘要与源码一致。

发布与 CI 成功不是逐系统安装或真实 SCM 验收；APT/DNF、SELinux、真实服务权限和物理故障恢复仍需专用测试环境。v0.5.0 的下载复核与只读检查保留为旧版记录，见文档发布页，不移作 v0.6.0 验收证据。

<a id="v06-开发中访问配置与维护"></a>

## v0.6.0：访问配置与维护

已发布中文安装、公网 IP HTTPS、自签证书管理、运行日志和 `qqon` 快捷维护。新装默认 `public-selfsigned`（用户确认公网 IPv4、HTTPS `8443`），也可选择仅本机、已有 HTTPS 代理或导入证书。发行协议保持 format3 九资产、用户数据格式仍为1；安装管理记录为 format2，不能与发行清单格式混淆。

- 已知 v0.4/v0.5 管理器须显式迁移，保留原本机／代理访问；先升级程序，再通过 `qqon config` 选择公网，不能在迁移时自动开放公网。
- 证书生成与 `qqon tls renew` 使用新目录，记录证书指纹和有效期；安装器和程序均检查主机名，不通过忽略 TLS 验证使探针成功。
- 自签证书有浏览器信任提示，须核对可信终端的 SHA-256 指纹。完成提示中的 URL 是配置结果，外网连通性未检测；云安全组、防火墙和 NAT 需人工配置。
- `qqon` 包装器由已审核安装器生成，只维护固定面板服务，不新增远程执行来源、任意管理员命令能力或发布资产。
- 备份先停服取得一致副本，包含账号和证书等敏感数据；配置和换证采用事务。旧 v0.4/v0.5 文件与既有访问不因新版本发布而被覆盖。

## 文档与程序分开发布

- `main` 上的文档和一键引导变更触发 `.github/workflows/docs.yml`，只发布静态站点。
- 程序 Release 由稳定标签 `vMAJOR.MINOR.PATCH` 触发 `.github/workflows/release.yml`。
- 文档可以先于 Release 更新，但必须显著标记尚未发布的能力。不要把没有发行文件的一键入口写成已经可安装。
- 当前 v0.6.0 与旧 v0.5.0 使用 format3、多平台九资产；旧版 v0.4.0 保留 format2、Linux 六资产。不要替换或移动已经公开的旧版本。

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
powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File scripts/windows-tests/test_maintenance.ps1 -PanelBinary C:\path\to\verified-test-panel.exe
```

执行策略仅对测试进程生效，不修改系统全局策略。真实服务注册、管理员 ACL、APT/DNF、SELinux、Windows Server 版本和物理故障恢复仍需专门测试环境，不能只看跨编译成功。

检查 `git diff --cached` 和文件清单；不上传私有账户、Token、初始化令牌、TOTP、测试凭据、机器信息或 `.zcode`。

## 创建发行

仅在另行确认下一版本、提交与变更说明后，设置实际稳定版本号 `VERSION` 再创建新标签。已发布的 v0.4.0、v0.5.0、v0.6.0 不要重打或移动：

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

需要 Go 1.26+、Python 3，使用实际完整 commit 与构建时间；以下在已设置 `BUILD_TIME` 后用于独立新目录，不覆盖正式 Release：

```sh
python3 -I tools/build_release.py \
  --version v0.6.0 --repo LinyuFiee/QQON-Panel \
  --commit 3cfd1a8fe75eca46fd1d991749c1be39f546638c \
  --build-time "$BUILD_TIME" \
  --output /absolute/existing-parent/new-release-directory
```

仅校验已有发行目录：

```sh
python3 -I tools/build_release.py --verify-only \
  --version v0.6.0 --repo LinyuFiee/QQON-Panel \
  --commit 3cfd1a8fe75eca46fd1d991749c1be39f546638c \
  --output /absolute/release-directory
```

开发机器上的合成 fixture 必须标为 test-only，不能当真实发行上传。`panel -build-info` 不创建账号或数据目录。

## 旧管理器迁移

v0.4.0 的更新器不认识九资产协议；v0.5.0 的旧管理器不因此自动获得 v0.6 访问配置和维护入口。新安装器只允许已识别官方旧管理器，经终端单独确认后备份并迁移；账户数据、原服务身份和访问方式不变，未完成事务先恢复。未知手动安装／改写过的管理器拒绝接管，不能通过改摘要、删锁或重置账户来绕过。

迁移成功后应用升级是单独操作；先确认新程序在旧访问方式下健康，再显式配置公网。详见文档的升级与迁移页。

## 信任与失败

- 一行安装命令信任 Pages、GitHub 仓库及发布权限；SHA-256 不是独立发布签名。
- 不运行整机升级，不禁用 TLS 或 SELinux 来“修复”安装。
- 构建／上传失败只留下未发布草稿或失败任务，不把半成品设为 Latest。
- 旧 Release 不覆盖，新修复发布新版本；数据迁移须另行设计，不能假称二进制回退恢复任意数据。
- 文档构建依赖审计信息见 `docs/guide/docs-development.md`，不公开 Vite 开发服务器；Pages 只部署静态产物。
