# QQON-Panel

轻量服务器面板：Go 单进程、原生 WebUI，少一点后台依赖，把资源留给业务。

[文档](https://linyufiee.github.io/QQON-Panel/) · [下载](https://github.com/LinyuFiee/QQON-Panel/releases/latest) · [发布指南](RELEASING.md)

> 当前稳定发行是 **v0.4.0**。本分支正在准备 **v0.5.0**：交互一键安装、更多 Linux 发行版及 Windows 原生支持。下方新入口在 v0.5.0 发布前会明确提示版本未就绪，不会用旧文件假装安装成功。项目仍处于早期阶段，请先在测试服务器验证。

## 一键交互安装 · v0.5.0

**Linux：在 root 终端执行**

```sh
curl -fsSL https://linyufiee.github.io/QQON-Panel/install.sh | bash
```

**Windows：在管理员 PowerShell 执行**

```powershell
irm https://linyufiee.github.io/QQON-Panel/install.ps1 | iex
```

复制一条命令后，在终端选择安装、升级或查看状态；首次可选择端口和本机／HTTPS 代理访问方式。仓库已内置，不必填写 `owner/repo`。

这些命令会执行本项目维护的引导代码，请确认域名和仓库来源。引导器把校验后的安装器下载到受保护的临时位置再运行，交互从终端读取。Linux 起始命令仍需 curl；只有 wget 的系统及不使用管道的审阅安装方式见[快速开始](https://linyufiee.github.io/QQON-Panel/guide/quickstart)。

**现在安装已发布 v0.4.0**：[旧版安装说明](https://linyufiee.github.io/QQON-Panel/guide/legacy-v0-4)。旧版与新管理器的迁移需要明确确认，不会覆盖账号或悄悄替换已发布文件。

## 支持范围

| 系统 | 架构 | v0.5.0 适配目标 |
| --- | --- | --- |
| Debian 12/13、Ubuntu 22.04/24.04 | amd64 / arm64 | APT、systemd、Linux 指标 |
| Rocky Linux 9/10、AlmaLinux 9/10 | amd64 / arm64 | DNF、systemd、CA 与账号工具适配 |
| Windows Server 2019/2022/2025、Windows 10/11 | x64 | 原生 SCM 服务、NTFS 权限、Windows 指标 |

实现范围不等于每个系统版本都已实机验收，详见[支持矩阵](https://linyufiee.github.io/QQON-Panel/guide/support-matrix)。Windows Docker 暂不开放；非 systemd 容器和停止维护的 Linux 版本不在首轮范围内。

## 功能

- **资源概览**：CPU、内存、磁盘、网络、运行时间，固定容量趋势与面板自身用量。
- **个人与安全**：管理员初始化、改密、TOTP、一次性恢复码、会话撤销、审计。
- **文件**：授权根目录内只读浏览／文本预览，限制路径、大小和特殊文件。
- **Linux 服务与 Docker**：白名单 systemd 状态、显式 Unix socket、受控容器操作。
- **应用源**：APT／Docker CE／Registry 分开配置；不冒充已经安装业务软件。
- **版本更新**：手动检查 GitHub Release，管理员终端升级，校验、备份、回退与中断恢复。

网站部署、Compose、应用商店、文件编辑、终端、数据库管理和自动证书尚未实现。真实数据缺失时显示不可用，不填充模拟状态。

## 轻量与安全

- 单个面板程序，无常驻 Node、Redis 或数据库。Go 官方 `x/sys` 提供 Windows 系统接口，不要求目标机器安装 Go。
- 共享周期采样、有界历史、有限请求与连接数；登录派生、任务队列、日志和文件输出均设上限。
- Linux 仅补齐缺失的必要依赖，不整机升级、不改源、不关闭 SELinux、不强装数据库／Docker。
- Windows 原生服务，不依赖 WSL 或 Docker Desktop；安装器以管理员运行，面板使用受限服务身份。
- 默认回环监听，没有默认管理员密码；公网访问应使用独立 HTTPS 来源。初始化令牌、TOTP 和备份必须保持私有。
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
