# 维护与发布

仓库：[LinyuFiee/QQON-Panel](https://github.com/LinyuFiee/QQON-Panel) · 文档：[GitHub Pages](https://linyufiee.github.io/QQON-Panel/)

[v0.7.0 发布 CI #42383705774](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42383705774) 已完成，`test-build`、`windows-test`、`publish` 全部成功并公开 Release，目标提交为 `5d668b229ae57055cb7667e996e50e58c116abba`。九个公开资产已独立下载并通过等价 `verify-only` 校验：GitHub 资产 SHA、归档、内层摘要和 Windows build-info 的版本／提交／构建时间 `2026-11-07T14:27:21Z` 一致；标题修正前后标签与资产身份不变。这些不是全平台实机验收或 bootstrap latest 路径的实测结论。

<a id="当前发行v050"></a>

## 当前已发布预览版：v0.7.0

- 当前下载／`latest` 为 [v0.7.0 预览版 Release](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.7.0)，发行提交固定为 `5d668b229ae57055cb7667e996e50e58c116abba`。
- 发布标题为 **QQON-Panel v0.7.0 预览版**。GitHub `prerelease=false` 保持既有 `latest` 协议；平台标志不代表产品稳定成熟。
- 发行清单保持 format3 九资产、用户数据 schema 1；**安装管理记录**为 format3，新增 `privilegeMode`／`privilegeConsent`，与发行清单不是同一文件。
- 本地三平台构建与公开九资产校验均按上述记录通过，验证的是产物身份和协议，不等于目标机安装；发布 CI、阶段 CI 与实际软件／网站测试须分开解释。
- 旧 v0.4.0／v0.5.0／v0.6.0 均为预览版，且没有真实业务软件安装和网站部署；标签、目标提交、资产及历史发布记录保持不变。

## v0.7.0：授权管理与托管

完整操作见 [托管指南](docs/guide/hosting.md) 与 [安装说明](docs/guide/installation.md)。

- Linux root／Windows LocalSystem 必须独立显式同意：`--accept-system-management`／`-AcceptSystemManagement`，或 `qqon privilege`；交互短语为 `同意最高权限`。普通 yes、update、root/管理员身份本身不授权，旧安装不自动提权。
- Linux 白名单软件与静态／代理／PHP 网站共用有界任务队列，使用现有可信 APT/DNF 系统源，不自动应用源模板。`install` 只安装并查询，未运行时单独 `start`，不承诺业务软件自动开机启用。
- 网站文件限受管根，上传 2 MiB／UTF-8 编辑 256 KiB；配置经 `nginx -t`、reload 和本机标记检查。删除站点保留文件。
- 网站 TLS 导入已有 PEM 并校验全部站点域名，不做 ACME；Windows 的 Linux 软件／建站为 `not_supported`，LocalSystem 不改变平台边界。
- 面板自身 RAM 与 Nginx／PHP／数据库／业务 RAM 分开，不以空面板指标承诺托管负载。

Linux 使用 `sudo qqon privilege [--accept-system-management] [--yes]`；已审阅 v0.7.0 安装器的新装仅本机示例为 `sudo bash install.sh --version v0.7.0 --mode local --yes --accept-system-management`。安装器 protocol 5 支持参数转发；普通升级／状态／管理器迁移不能夹带授权标志。

Windows 使用 `qqon privilege` 或 `qqon privilege --accept-system-management --yes`；完整带引号的 PowerShell 路径、新装和迁移参数见 [Windows 服务](docs/guide/windows-services.md#privilege-commands)。

## 已验证记录与仍有的限制

- **主程序阶段 CI**：[42380757509](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42380757509)，提交 `fa1483f9ec57294d000a6f71014c4e6ee7afe87c`，Linux unit／race／真实托管与 Windows Go＋57 项 PowerShell 成功。该提交不是发行标签提交，不冒充本次 Release CI。
- **此前 Ubuntu 场景记录**：[42379665605](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42379665605)，提交 `80fea3b505bc429541ba31ed2f1ac751debaaf9e`。实际缺包安装并启动 Nginx `1.24.0-2ubuntu7.19`／PHP `8.3.6-0ubuntu0.24.04.13`；静态／代理／PHP HTTP、上传编辑、停用／启用、损坏 Nginx 配置 fixture 回退与重启、删除保留文件、认证／CSRF／同意撤销通过。
- **Windows 57/57**：install 23、maintenance 18、privilege 16，本机和阶段 CI 均为 TEMP/mock ACL/SCM；不是真实 LocalSystem、SCM 注册或完整 NTFS ACL 验收。Linux 安装器首轮 135 项（含 10 privilege）仅保留为隔离阶段记录。
- **两次短时 RSS**：各 5 次操作后 process RSS 采样，`80fea3b…`／42379665605 中位数＝最大值 **15.42 MiB**；`fa1483f9…`／42380757509 中位数 **15.51 MiB**、最大值 **15.52 MiB**。不含业务进程，不是发行标签的新测试、SLA、soak 或全负载结论。
- **未覆盖**：MariaDB／Redis 仅 fake 逻辑测试、未真实安装；DNF 未真实测试；网站 HTTPS 导入只有单元／loopback 测试，未验证真实 Nginx HTTPS。GUI 仅登录截图通过，后续截图工具超时，整体 GUI 验收未完成。

预览发行不抹除这些空白。逐系统、完整旧机迁移与权限回退、真实 Windows SCM/LocalSystem、SELinux、物理故障恢复及长期负载仍需专用环境验证；不在既有业务服务器试错。

<a id="v06-开发中访问配置与维护"></a>

## 历史 v0.6.0：访问配置与维护

[v0.6.0 预览版](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.6.0) 的 tag 为 `3cfd1a8fe75eca46fd1d991749c1be39f546638c`，历史 [发布 CI 42349315659](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42349315659) 三项任务成功。它引入中文安装、公网自签 HTTPS、运行日志和 `qqon` 维护，但没有真实业务软件安装和建站；安装管理记录是 format2。

v0.6 发布时的更新检查、Windows format3 元数据检查及 Pages HTTPS 200／摘要复核保留为历史记录，不移作 v0.7 验收。v0.4/v0.5 的回环／代理访问、旧资产和账号不因新版本发布被覆盖；更多历史见 [发布记录](docs/guide/releases.md)。

## 文档与程序分开发布

- `main` 上的文档与一键引导变更触发 `.github/workflows/docs.yml`，只发布静态站点。
- 程序 Release 由 `vMAJOR.MINOR.PATCH` 标签触发 `.github/workflows/release.yml`；标签格式或 `latest` 不代表成熟度。
- 发布后文稿可先在本地准备，但必须等维护者确认 Release 已公开、版本／提交／完整资产／`latest` 正确，再推送 Pages；本地构建不发布程序或文档。
- v0.5/v0.6/v0.7 均为 format3 九资产；v0.4 保留 format2 六资产。所有已发布 tag 和资产不可变，不移动标签或替换旧附件。

## 发版前检查

```sh
go test ./...
go vet ./...
python3 -I -m unittest discover -s scripts/tests
python3 -I -m unittest discover -s tools/tests
npm --prefix docs ci
npm --prefix docs run build
npm --prefix docs run check:links
```

Windows 原生 PowerShell 隔离测试使用仓库已有的 install／maintenance／privilege 测试。执行策略只对测试进程生效，不改变系统全局策略；真实服务、ACL 与恢复需独立验收。

检查 `git diff --cached` 和文件清单；不上传私有账户、Token、初始化令牌、TOTP、测试凭据、机器信息或 `.zcode`。

## 创建发行与不可变性

本次 v0.7.0 已获针对提交 `5d668b229ae57055cb7667e996e50e58c116abba` 的发布授权，由主代理推送标签并执行现有 Release CI。新版本仍须另行确认版本、确切提交与变更说明；已公开的 v0.4.0／v0.5.0／v0.6.0／v0.7.0 不重打、不移动、不覆盖资产。

Actions 验证标签身份，运行测试与构建，安全解压验证 build-info；发布任务仅在检查通过后创建草稿、上传完整资产、复核再公开。发布失败不把半成品设为 Latest；不要把阶段 CI 成功写成发布成功。

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

用户数据 schema 仍为1；Windows 与 Linux 程序／安装器不互换，新读取器能识别旧 format2，不让 Windows 安装旧 Linux 产物。发布脚本严格固定资产集、有限解压，只输出到新目录，不执行 artifact 下载的程序。

## 本地打包

Go 1.26+、Python 3，在发行目标提交且已设置实际 `BUILD_TIME` 后输出到独立新目录，不覆盖已公开 Release，也不给其他源码冒用此提交身份：

```sh
python3 -I tools/build_release.py \
  --version v0.7.0 --repo LinyuFiee/QQON-Panel \
  --commit 5d668b229ae57055cb7667e996e50e58c116abba \
  --build-time "$BUILD_TIME" \
  --output /absolute/existing-parent/new-release-directory

python3 -I tools/build_release.py --verify-only \
  --version v0.7.0 --repo LinyuFiee/QQON-Panel \
  --commit 5d668b229ae57055cb7667e996e50e58c116abba \
  --output /absolute/release-directory
```

合成 fixture 必须标为 test-only，不能当真实发行上传。`panel -build-info` 不创建账号或数据目录。

## 旧管理器迁移

先下载、校验和审阅官方 v0.7.0 管理器，再显式迁移：Linux 仅已知 v0.4/v0.5/v0.6；Windows 仅 v0.5/v0.6，无 Windows v0.4。未完成事务先恢复，未知或修改过的管理器拒绝接管。

**管理器迁移 → `qqon update` 升级程序、保持受限并确认健康 → 单独 `qqon privilege` 授权**。迁移保留原访问、账号和私有数据，普通 yes/update 不同意 root／LocalSystem；公网模式变化另走 `qqon config`，不自动开放旧安装。

## 信任与失败

- 一行安装信任 Pages、GitHub 仓库及发布权限；SHA-256 不是独立发布签名。
- 不整机升级，不关闭 TLS、SELinux 或 ACL 检查来“修复”安装。
- 数据迁移须独立设计，二进制回退不是任意数据恢复保证。
- VitePress 1.6.4 与 lockfile 不变；完整 `npm audit` 当前记录 **3 项（2 moderate、1 high）**，不能称为漏洞 clean。不公开 Vite 开发服务器，Pages 仅部署静态产物，见 [文档构建边界](docs/guide/docs-development.md)。
