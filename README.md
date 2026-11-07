# QQON-Panel

轻量服务器面板：Go 单进程、原生 WebUI，少一点后台依赖，把资源留给业务。

[文档](https://linyufiee.github.io/QQON-Panel/) · [下载](https://github.com/LinyuFiee/QQON-Panel/releases/latest) · [发布指南](RELEASING.md)

> **[v0.7.0 预览版](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.7.0) 已发布**：独立授权 root／LocalSystem、Linux 软件安装、静态／代理／PHP 网站、有限上传编辑及后台任务；保留中文安装、四种访问模式与 `qqon` 维护。发行提交为 `5d668b229ae57055cb7667e996e50e58c116abba`。
>
> **预览版不等于稳定成熟或全平台实机验收。** 旧 v0.4.0／v0.5.0／v0.6.0 均为已发布预览版，没有真实业务软件安装和网站部署；其标签、资产与历史说明保留不变。当前 v0.7.0 的能力与限制见 [托管指南](docs/guide/hosting.md)。

<a id="一键交互安装--v050"></a>

## 一键交互安装 · v0.7.0 预览版

**Linux：在 root 终端执行**

```sh
curl -fsSL https://linyufiee.github.io/QQON-Panel/install.sh | bash
```

**Windows：在管理员 PowerShell 执行**

```powershell
irm https://linyufiee.github.io/QQON-Panel/install.ps1 | iex
```

中文向导默认 `public-selfsigned`：确认自己的公网 IPv4，使用 HTTPS `8443`；也可选择仅本机、已有 HTTPS 代理或导入证书。仓库已内置，不必填写 `owner/repo`。安装后运行 `qqon` 打开中文维护菜单。

**最高系统权限另行同意**：阅读专用告知后输入 `同意最高权限`，或使用新装专用 `--accept-system-management`／`-AcceptSystemManagement`。拒绝时保留受限模式；普通 yes、root/管理员身份与 update 均不授权。Windows 即使授权 LocalSystem，也不支持 Linux 业务软件和 Nginx 建站。

自签证书会有浏览器警告，请与可信终端显示的 SHA-256 指纹核对。安装结果中的公网链接只是配置结果，**未检测外网可达性**；云安全组、防火墙和 NAT 需人工配置。详见[公网 HTTPS](https://linyufiee.github.io/QQON-Panel/guide/public-https)与[维护命令](https://linyufiee.github.io/QQON-Panel/guide/maintenance)。

这些命令会执行本项目维护的引导代码，请确认域名和仓库来源。引导器把校验后的安装器下载到受保护的临时位置再运行，交互从终端读取。Linux 起始命令仍需 curl；wget 与先下载审阅的安装方式见[安装说明](https://linyufiee.github.io/QQON-Panel/guide/installation)。

**旧安装升级顺序**：先下载、校验并审阅 v0.7.0 管理器，显式迁移已识别旧管理器 → `qqon update` 升级程序并保持受限、确认健康 → 如需最高权限，再独立 `qqon privilege`。保留原访问、账号和私有数据，不自动开放公网。Linux 识别官方 v0.4/v0.5/v0.6，Windows 仅 v0.5/v0.6，无 Windows v0.4 支持。[迁移说明](https://linyufiee.github.io/QQON-Panel/guide/upgrade-migration)

## 支持范围

| 系统 | 架构 | v0.7.0 发行范围与限制 |
| --- | --- | --- |
| Debian 12/13、Ubuntu 22.04/24.04 | amd64 / arm64 | APT、systemd、Linux 指标与授权托管；真实测试仅覆盖列出的 Ubuntu 场景 |
| Rocky Linux 9/10、AlmaLinux 9/10 | amd64 / arm64 | DNF 安装适配；DNF／SELinux 尚未真实验收 |
| Windows Server 2019/2022/2025、Windows 10/11 | x64 | 原生 SCM 服务、NTFS 权限、Windows 指标；Linux 软件／网站操作 `not_supported` |

逐系统安装、真实 Windows SCM/LocalSystem、SELinux 与物理故障恢复仍未完成全量验证，详见[支持矩阵](https://linyufiee.github.io/QQON-Panel/guide/support-matrix)。Windows Docker 暂不开放；非 systemd 容器和停止维护的 Linux 版本不在范围内。

## 功能与边界

- **资源概览**：CPU、内存、磁盘、网络、运行时间，固定容量趋势与面板自身用量。
- **个人与安全**：管理员初始化、改密、TOTP、一次性恢复码、会话撤销、审计。
- **文件**：既有“文件”页为授权根内只读浏览／预览；网站上传编辑只作用于面板受管站点根。
- **Linux 服务与 Docker**：白名单 systemd 状态、显式 Unix socket、受控容器操作。
- **应用与源**：APT／Docker CE／Registry 的配置与模板分开管理，不会自动应用系统源；真实软件安装走独立“软件”页。
- **版本更新与维护**：手动检查 Release；管理员通过 `qqon` 查看状态、维护固定服务、更新、备份、配置、独立授权及面板自签证书续期（`qqon tls renew`）。

### v0.7.0 软件、网站与任务

- **独立最高权限同意**：Linux `--accept-system-management`、Windows `-AcceptSystemManagement`，或 `qqon privilege`。普通 `--yes`／`-Yes`、`qqon update`、管理员/root 启动或网页登录都不构成授权。授权后是面板进程本身以 root／LocalSystem 运行，失陷可影响整台主机。
- **Linux 软件与任务**：白名单 Nginx、PHP-FPM、MariaDB、Redis，使用现有可信 APT/DNF 源。`install` 仅安装并查询，不额外 `enable`／`start`；安装后查看状态，对未运行的 Nginx／PHP-FPM 显式 `start`、确认运行后再建站，不承诺自动开机启用。
- **Linux 网站**：静态、反向代理与 PHP；受管配置先 `nginx -t`，再 reload 与本机站点标记校验。单次上传最多 2 MiB、UTF-8 编辑最多 256 KiB；删除站点保留文件。
- **TLS 与平台限制**：网站 TLS 导入已有 PEM 证书／私钥，校验全部站点域名，不做 ACME 自动签发／续期；面板 TLS 与网站 TLS 分开管理。Windows 软件／建站仍为 `not_supported`。

Compose、完整应用商店、通用终端、数据库管理、任意系统文件编辑和 ACME 不在 v0.7.0 范围内。真实数据缺失时显示不可用，不填充模拟状态；完整流程见 [托管指南](docs/guide/hosting.md)。

## 已验证范围

- **发行 CI 已成功**：[run #42383705774](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42383705774)，提交 `5d668b229ae57055cb7667e996e50e58c116abba`，test-build／windows-test／publish 成功；公开九资产独立下载校验通过，不代表全平台实机验收。
- **主程序阶段 CI**：[run #42380757509](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42380757509)，提交 `fa1483f9ec57294d000a6f71014c4e6ee7afe87c`。Linux unit／race／真实托管及 Windows Go＋57 项 PowerShell 成功；这是发行前阶段记录，不冒充 v0.7.0 标签的发布 CI。
- **此前 Ubuntu 真实托管记录**：[run #42379665605](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42379665605)，提交 `80fea3b505bc429541ba31ed2f1ac751debaaf9e`。覆盖缺包安装并启动 Nginx／PHP-FPM、静态／代理／PHP HTTP、上传编辑、站点停用／启用、损坏 Nginx 配置 fixture 回退与重启、删除保留文件、认证／CSRF／同意撤销。
- **Windows PowerShell 57/57**：install 23 + maintenance 18 + privilege 16；本机及上述阶段 CI 使用 TEMP/mock ACL/SCM，不是真实 LocalSystem／SCM 安装验收。
- **保留的空白**：MariaDB／Redis 仅 fake 逻辑测试、未真实安装；DNF 未真实验证；网站 HTTPS 导入仅单元／loopback 测试，未验证真实 Nginx HTTPS。GUI 仅登录截图通过，后续截图工具超时，完整 GUI 验收未完成。

发行文件的构建和发布不自动扩大这些验证范围，详见 [发布状态](docs/guide/releases.md)。

## 轻量与安全

- 单个面板程序，无常驻 Node、Redis 或数据库依赖。Windows 使用官方 `x/sys`，目标机器不需 Go。
- 共享周期采样、有界历史，请求、任务、连接、日志和文件输出均有上限。
- 面板安装只补缺失的必要依赖，不整机升级、不改源、不关闭 SELinux、不强装数据库／Docker；业务软件另需系统管理授权与单项确认。
- 未授权时使用受限服务身份；仅独立授权才切换 root／LocalSystem，普通升级不改变权限模式。
- 无默认管理员密码；初始化令牌、TOTP、证书私钥和备份保持私有。更新不通过网页提权，不重置账户。
- 压缩包与内层程序分别验证 SHA-256、大小、架构和版本，拒绝危险归档。SHA-256 不等于独立发布签名，仍须保护 GitHub 发布权限。

Go 堆、面板进程 RSS／工作集与主机总 RAM 是不同指标。Nginx、PHP-FPM、MariaDB、Redis 和业务进程不计入面板自身占用，但占用主机内存。

两次**各 5 次操作后短时 process RSS 采样**分别记录：`80fea3b…`／CI `42379665605` 中位数＝最大值 **15.42 MiB**；`fa1483f9…`／CI `42380757509` 中位数 **15.51 MiB**、最大值 **15.52 MiB**。两者均不含业务进程，不是发行标签的新测量、SLA、soak 或整套负载上限。

## 开发

面板构建需要 **Go 1.26+**。文档使用 VitePress，Node/npm 只用于文档构建，不安装到面板用户服务器。

```sh
go test ./...
go vet ./...
go build -trimpath -ldflags="-s -w" -o panel .
python3 -I -m unittest discover -s scripts/tests
python3 -I -m unittest discover -s tools/tests
go test -race ./...  # 需受支持的系统与 C 工具链
npm --prefix docs ci
npm --prefix docs run build
npm --prefix docs run check:links
```

Windows 产物为 `panel.exe`；控制台仍需合格私有数据目录，服务安装通过匹配版本 PowerShell 安装器完成。隔离测试位于 `scripts/windows-tests/`，不修改开发主机服务；跨编译和模拟测试不代替实机安装、权限与恢复验收。

文档保持 VitePress 1.6.4 和既有 lockfile，完整 `npm audit` 当前记录 **3 项（2 moderate、1 high）**，不因只发布静态页面就视为无漏洞；不公开开发／预览服务，见 [文档构建与审计边界](docs/guide/docs-development.md)。

本轮未引入 [Hairline](https://github.com/lucasmarkes/hairline) 的第三方代码或资产，原生 UI 不增加该依赖；其公开元数据与 LICENSE 已核实为 MIT，定位是线条交互组件而非完整面板，不代表 QQON-Panel 采用该许可证。

当前未指定开源许可证；仓库公开不代表授予任意再分发许可。
