# QQON-Panel

轻量服务器面板：Go 单进程、原生 WebUI，少一点后台依赖，把资源留给业务。

[文档](https://linyufiee.github.io/QQON-Panel/) · [下载](https://github.com/LinyuFiee/QQON-Panel/releases/latest) · [发布指南](RELEASING.md)

> **[v0.6.0 预览版](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.6.0) 已发布**：中文安装向导、默认公网 IPv4 自签 HTTPS、四种访问模式与统一 `qqon` 维护。下方下载与安装仍对应当前 v0.6.0，不是 v0.7。
>
> **v0.4.0、v0.5.0、v0.6.0 均按预览版定位**，不代表稳定成熟或全平台实机验收完成；这些已发布版本没有真实业务软件安装、网站创建与部署能力。“应用与源”是配置／模板能力，不是应用已经安装。
>
> **v0.7 预览开发中，尚未发布。** 已有 Ubuntu 真实托管 CI 和 Windows 本机隔离测试记录，但不是全平台验收。本轮开发显式授权 root／LocalSystem、Linux 软件安装与网站／文件／任务流程，见 [v0.7 托管开发指南](docs/guide/hosting.md)。不要为它构造 Release 下载地址，也不要把当前安装命令当作 v0.7 安装入口。

<a id="一键交互安装--v050"></a>

## 一键交互安装 · v0.6.0 预览版

**Linux：在 root 终端执行**

```sh
curl -fsSL https://linyufiee.github.io/QQON-Panel/install.sh | bash
```

**Windows：在管理员 PowerShell 执行**

```powershell
irm https://linyufiee.github.io/QQON-Panel/install.ps1 | iex
```

中文向导默认 `public-selfsigned`：确认自己的公网 IPv4，使用 HTTPS `8443`；也可选择仅本机、已有 HTTPS 代理或导入证书。仓库已内置，不必填写 `owner/repo`。安装后运行 `qqon` 打开中文维护菜单。

自签证书会有浏览器警告，请与可信终端显示的 SHA-256 指纹核对。安装结果中的公网链接只是配置结果，**未检测外网可达性**；云安全组、防火墙和 NAT 需人工配置。详见[公网 HTTPS](https://linyufiee.github.io/QQON-Panel/guide/public-https)与[维护命令](https://linyufiee.github.io/QQON-Panel/guide/maintenance)。

这些命令会执行本项目维护的引导代码，请确认域名和仓库来源。引导器把校验后的安装器下载到受保护的临时位置再运行，交互从终端读取。Linux 起始命令仍需 curl；只有 wget 的系统及不使用管道的审阅安装方式见[安装说明](https://linyufiee.github.io/QQON-Panel/guide/installation)。

**旧版 v0.4 / v0.5** 保留原回环／代理访问。升级 v0.6.0 须显式迁移已知管理器、再升级程序，之后才可通过 `qqon config` 选择公网模式；不自动开放旧安装或覆盖账号。[v0.4 固定版本说明](https://linyufiee.github.io/QQON-Panel/guide/legacy-v0-4) · [迁移说明](https://linyufiee.github.io/QQON-Panel/guide/upgrade-migration)

## 支持范围

| 系统 | 架构 | v0.6.0 已发布范围 |
| --- | --- | --- |
| Debian 12/13、Ubuntu 22.04/24.04 | amd64 / arm64 | APT、systemd、Linux 指标 |
| Rocky Linux 9/10、AlmaLinux 9/10 | amd64 / arm64 | DNF、systemd、CA 与账号工具适配 |
| Windows Server 2019/2022/2025、Windows 10/11 | x64 | 原生 SCM 服务、NTFS 权限、Windows 指标 |

[发布 CI #42349315659](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42349315659) 的 `test-build`、`windows-test`、`publish` 均成功，包含新增 Windows maintenance 隔离测试。具体系统安装、真实 SCM、SELinux 与物理故障恢复仍未验证，详见[支持矩阵](https://linyufiee.github.io/QQON-Panel/guide/support-matrix)。Windows Docker 暂不开放；非 systemd 容器和停止维护的 Linux 版本不在首轮范围内。

## 已发布预览版的功能

以下描述 v0.6.0 及旧版边界，不包含尚未发布的 v0.7 托管能力。

- **资源概览**：CPU、内存、磁盘、网络、运行时间，固定容量趋势与面板自身用量。
- **个人与安全**：管理员初始化、改密、TOTP、一次性恢复码、会话撤销、审计。
- **文件**：授权根目录内只读浏览／文本预览，限制路径、大小和特殊文件。
- **Linux 服务与 Docker**：白名单 systemd 状态、显式 Unix socket、受控容器操作。
- **应用源**：APT／Docker CE／Registry 的配置与模板分开管理；不会执行真实软件安装或自动应用模板。
- **版本更新与维护**：手动检查 Release，管理员通过 `qqon` 查看状态、管理固定服务、升级、备份、配置与自签证书续期（`qqon tls renew`）。

v0.4.0／v0.5.0／v0.6.0 没有真实软件安装、网站部署或文件编辑；Compose、完整应用商店、终端、数据库管理和自动申请公共 CA 证书也不在这些版本范围内。真实数据缺失时显示不可用，不填充模拟状态。

## v0.7 预览开发中的托管能力

- **独立最高权限同意**：Linux `--accept-system-management`、Windows `-AcceptSystemManagement`，或新维护入口 `qqon privilege`；交互须确认 `同意最高权限`。普通 `--yes`／`-Yes`、`qqon update`、管理员/root 启动或网页登录都不构成授权。授权后是面板进程本身以 root／LocalSystem 运行，失陷可影响整台主机；未授权保留受限模式。
- **Linux 软件与任务**：按白名单管理 Nginx、PHP-FPM、MariaDB、Redis，使用主机现有可信 APT/DNF 源，不把“应用与源”模板当成已应用的系统源。`install` 仅安装并查询，不额外执行 `enable`／`start`；安装后查看状态，对未运行的 Nginx／PHP-FPM 显式 `start`、确认运行后再建站，不承诺自动开机启用。
- **Linux 网站**：静态、反向代理与 PHP 站点；写入受管配置后先 `nginx -t`、再 reload 与本机站点标记校验。文件只在受管站点根内，单次上传最多 2 MiB、UTF-8 文本编辑最多 256 KiB；删除站点保留网站文件。
- **平台与证书限制**：Windows 即使授权 LocalSystem 也不支持上述 Linux 软件安装和 Nginx 建站；网站 TLS 为已有 PEM 证书／私钥导入，不是 ACME 自动签发或续期。面板 TLS 与网站 TLS 分开管理。

以上能力仍未发布；匹配版本的命令前提与失败边界见 [托管开发指南](docs/guide/hosting.md)。本轮已验证范围如下，不外推为全平台或生产承诺：

- **Ubuntu 真实托管 CI 通过**：[run #42379665605](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42379665605)，提交 `80fea3b505bc429541ba31ed2f1ac751debaaf9e`。覆盖缺包安装并启动 Nginx／PHP-FPM、静态／代理／PHP HTTP、上传编辑、网站停用／启用、损坏 Nginx 配置 fixture 的失败回退与重启、删除保留文件、认证／CSRF／同意撤销。
- **Windows 本机 PowerShell 57/57**：install 23 + maintenance 18 + privilege 16；使用 TEMP 与 mock ACL/SCM，不是真实 LocalSystem／SCM 安装验收。
- **最新全合并代码 CI 通过**：[run #42380757509](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42380757509)，提交 `fa1483f9ec57294d000a6f71014c4e6ee7afe87c`；Linux unit／race／真实托管及 Windows Go＋57 项 PowerShell 均成功，Windows 仍是隔离测试而非真实 SCM／LocalSystem 安装，v0.7 仍未发布。
- **仍有明确空白**：MariaDB／Redis 仅 fake 逻辑测试，未真实安装；DNF 未真实验证；网站 HTTPS 导入仅单元／loopback 测试，未验证真实 Nginx HTTPS。GUI 仅登录截图通过，后续截图工具超时，未完成整体 GUI 验收。

## 轻量与安全

- 单个面板程序，无常驻 Node、Redis 或数据库。Go 官方 `x/sys` 提供 Windows 系统接口，不要求目标机器安装 Go。
- 共享周期采样、有界历史、有限请求与连接数；登录派生、任务队列、日志和文件输出均设上限。
- Linux 面板安装仅补齐缺失的必要依赖，不整机升级、不改源、不关闭 SELinux、不强装数据库／Docker。v0.7 开发中的业务软件安装另需系统管理授权与单项操作确认。
- Windows 原生服务，不依赖 WSL 或 Docker Desktop；v0.6 安装器以管理员运行，面板使用受限服务身份。v0.7 只有独立授权才切换为 LocalSystem，不因普通升级改变身份。
- 新装默认公网 IP 自签 HTTPS，须确认地址和证书指纹；旧版迁移保留原访问。没有默认管理员密码，初始化令牌、TOTP、证书私钥和备份必须保持私有。
- 压缩包与内层程序分别验证 SHA-256、大小、架构和版本，解压拒绝链接、特殊文件、额外成员和超限内容。
- 更新不通过网页提权，不重置账户。SHA-256 校验不等同独立发布签名，仍须保护 GitHub 发布权限。

低配资源占用需要在实际业务负载下测量。Go 堆、面板进程工作集／RSS 和主机总内存不能混为一个指标；Nginx、PHP-FPM、MariaDB、Redis 与用户业务的 RAM 属于工作负载，不计入“面板自身占用”，但会占用主机内存。面板低占用不代表装好网站与数据库后仍有相同内存余量。

上述 `80fea3b505bc429541ba31ed2f1ac751debaaf9e` 的 Ubuntu CI 中，**5 次操作后短时采样**的面板进程 RSS 中位数与最大值均为 **15.42 MiB**。这是该次指定测试场景的数据，不移作后续 `fa1483f9…` CI 的测量，不包含业务进程内存，不是 SLA、soak 长时间运行测试或整套负载上限。

## 开发

面板构建需要 **Go 1.26+**。文档使用 VitePress，Node/npm 只用于文档构建，不安装到面板用户服务器。

```sh
go test ./...
go vet ./...
go build -trimpath -ldflags="-s -w" -o panel .
```

Windows 产物使用 `panel.exe`。仅以控制台运行时，仍须使用权限合格的私有数据目录；系统服务安装通过匹配版本的 PowerShell 安装器完成。

```sh
python3 -I -m unittest discover -s scripts/tests
python3 -I -m unittest discover -s tools/tests
go test -race ./...  # 需受支持的系统与 C 工具链
npm --prefix docs ci
npm --prefix docs run build
npm --prefix docs run check:links
```

文档保持既有 VitePress 1.6.4 依赖。当前记录的完整 `npm audit` 为 **3 项告警（2 moderate、1 high）**，没有因只发布静态页面就视为无漏洞；不公开开发／预览服务，详见 [文档构建与审计边界](docs/guide/docs-development.md)。

Windows 安装器的隔离测试位于 `scripts/windows-tests/`，不会修改开发主机的系统服务。CI、跨编译和模拟测试不能替代全部支持系统的真实安装、权限和恢复验收。

[安装与升级](https://linyufiee.github.io/QQON-Panel/guide/installation) · [安全设置](https://linyufiee.github.io/QQON-Panel/guide/security) · [旧版迁移](https://linyufiee.github.io/QQON-Panel/guide/upgrade-migration)

本轮未引入 [Hairline](https://github.com/lucasmarkes/hairline) 的第三方代码或资产，原生 UI 不增加该依赖；其公开仓库元数据与 LICENSE 已核实为 MIT，定位是线条交互组件而非完整面板，不代表 QQON-Panel 采用该许可证。

当前未指定开源许可证；仓库公开不代表授予任意再分发许可。
