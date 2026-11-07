# QQON-Panel

轻量服务器面板：Go 单进程、原生 WebUI，少一点后台依赖，把资源留给业务。

[文档](https://linyufiee.github.io/QQON-Panel/) · [下载](https://github.com/LinyuFiee/QQON-Panel/releases/latest) · [发布指南](RELEASING.md)

> **[v0.6.0 正式版](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.6.0) 已发布**：中文安装向导、默认公网 IPv4 自签 HTTPS、四种访问模式与统一 `qqon` 维护。选择系统，执行下方命令后按提示操作。

<a id="一键交互安装--v050"></a>

## 一键交互安装 · v0.6.0

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

## 功能

- **资源概览**：CPU、内存、磁盘、网络、运行时间，固定容量趋势与面板自身用量。
- **个人与安全**：管理员初始化、改密、TOTP、一次性恢复码、会话撤销、审计。
- **文件**：授权根目录内只读浏览／文本预览，限制路径、大小和特殊文件。
- **Linux 服务与 Docker**：白名单 systemd 状态、显式 Unix socket、受控容器操作。
- **应用源**：APT／Docker CE／Registry 分开配置；不冒充已经安装业务软件。
- **版本更新与维护**：手动检查 Release，管理员通过 `qqon` 查看状态、管理固定服务、升级、备份、配置与自签证书续期（`qqon tls renew`）。

网站部署、Compose、应用商店、文件编辑、终端、数据库管理和自动申请公共 CA 证书尚未实现。真实数据缺失时显示不可用，不填充模拟状态。

## 轻量与安全

- 单个面板程序，无常驻 Node、Redis 或数据库。Go 官方 `x/sys` 提供 Windows 系统接口，不要求目标机器安装 Go。
- 共享周期采样、有界历史、有限请求与连接数；登录派生、任务队列、日志和文件输出均设上限。
- Linux 仅补齐缺失的必要依赖，不整机升级、不改源、不关闭 SELinux、不强装数据库／Docker。
- Windows 原生服务，不依赖 WSL 或 Docker Desktop；安装器以管理员运行，面板使用受限服务身份。
- 新装默认公网 IP 自签 HTTPS，须确认地址和证书指纹；旧版迁移保留原访问。没有默认管理员密码，初始化令牌、TOTP、证书私钥和备份必须保持私有。
- 压缩包与内层程序分别验证 SHA-256、大小、架构和版本，解压拒绝链接、特殊文件、额外成员和超限内容。
- 更新不通过网页提权，不重置账户。SHA-256 校验不等同独立发布签名，仍须保护 GitHub 发布权限。

低配资源占用需要在实际业务负载下测量。Go 堆、进程工作集／RSS 和主机总内存不能混为一个指标。

## 开发

面板构建需要 **Go 1.26+**。文档使用 VitePress，Node/npm 只用于文档构建，不安装到面板用户服务器。

```sh
go test ./...
go vet ./...
go build -trimpath -ldflags="-s -w" -o panel .
```

Windows 产物使用 `panel.exe`。仅以控制台运行时，仍须使用权限合格的私有数据目录；正式服务安装通过 PowerShell 安装器完成。

```sh
python3 -I -m unittest discover -s scripts/tests
python3 -I -m unittest discover -s tools/tests
go test -race ./...  # 需受支持的系统与 C 工具链
npm --prefix docs ci
npm --prefix docs run build
```

Windows 安装器的隔离测试位于 `scripts/windows-tests/`，不会修改开发主机的系统服务。CI、跨编译和模拟测试不能替代全部支持系统的真实安装、权限和恢复验收。

[安装与升级](https://linyufiee.github.io/QQON-Panel/guide/installation) · [安全设置](https://linyufiee.github.io/QQON-Panel/guide/security) · [旧版迁移](https://linyufiee.github.io/QQON-Panel/guide/upgrade-migration)

当前未指定开源许可证；仓库公开不代表授予任意再分发许可。
