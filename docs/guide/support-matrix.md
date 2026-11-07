# 支持矩阵与验证状态

::: warning “已发布 / CI 通过”不等于“实机验收”
当前正式 Release 是 **[v0.5.0](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.5.0)**。[发布 CI #42328127375](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42328127375) 的 `test-build`、`windows-test`、`publish` 均为 `success`。发布、CI 验证和具体系统安装是不同层次；不能据此宣称所有系统或真实 SCM 安装已完成验收。
:::

## 系统范围

| 系统 / 架构 | 实现与发行状态 | CI 验证边界 | 实机状态 |
| --- | --- | --- | --- |
| Debian 12、13 / amd64、arm64 | v0.5.0 Linux 产物与 APT、systemd 安装路径已发布 | `test-build` 成功；不等于逐版本 APT/systemd 安装验收 | **具体版本、架构安装与恢复仍未验证** |
| Ubuntu 22.04、24.04 / amd64、arm64 | v0.5.0 Linux 产物与 APT、systemd 安装路径已发布 | `test-build` 成功；Linux runner 不覆盖两个版本的全套安装/升级 | **具体版本、架构安装与恢复仍未验证** |
| Rocky Linux 9、10 / amd64、arm64 | v0.5.0 Linux 产物与 DNF 适配已发布 | `test-build` 成功；模拟包管理器不验证真实 DNF/SELinux/systemd | **具体版本安装、SELinux 与恢复仍未验证** |
| AlmaLinux 9、10 / amd64、arm64 | v0.5.0 Linux 产物与 DNF 适配已发布 | `test-build` 成功；不能用 Rocky 或同系测试替代 | **具体版本安装、SELinux 与恢复仍未验证** |
| Windows 10、11 / x64 | v0.5.0 原生程序、指标与服务安装器已发布 | `windows-test` 成功；Go / PowerShell 隔离测试不是实际 SCM 注册 | **真实 SCM、ACL、安装/升级/回退仍未验证** |
| Windows Server 2019、2022、2025 / x64 | v0.5.0 x64 产物与服务安装器已发布 | `windows-test` 成功；单个 runner 不覆盖三个 Server 版本 | **逐版本真实安装、权限与恢复仍未验证** |

上述 CI 对应正式 tag 提交 `07e3665b582ce68198704fa38180e15f135167d6`。九个附件重新下载后通过 `verify-only`；Windows bootstrap 只读 `Get-QPReleaseMetadata` 检查 v0.5.0 通过，本地面板版本检查为 `state=up_to_date`、`assetsReady=true`。这些结果确认发行校验与只读版本检查，不代表运行过真实安装器或完成 SCM、SELinux、物理断电验收。

## 必须满足的条件

### Linux

- root 安装权限、Bash、正在运行的 systemd；架构为 amd64 或 arm64。
- 能访问 GitHub HTTPS、有效 CA 证书、发行版现有软件源正常。
- 安装器所需 Python 至少 3.8；只补齐缺失的最小依赖。Linux 一行入口初始仍需要 `curl` 或 `wget`。
- 不支持仅有 BusyBox、没有活动 systemd 的容器；不会通过强装 systemd 来“支持”它们。
- 不自动升级整机、修改软件源、关闭 SELinux、开防火墙端口或安装 Docker。

### Windows {#windows-v0-5-待发布}

- x64 Windows，管理员 Windows PowerShell 5.1、NTFS、本机服务控制管理器（SCM）。
- 使用系统 PowerShell/.NET 的 HTTPS、ZIP 和 SHA-256 能力；用户机器不需 Python、WSL、Go 或 Node。
- ARM64、32 位 Windows、非 NTFS、Wine 不在本轮安装范围内。不能靠关闭证书校验、全局执行策略或 ACL 检查绕过限制。

## 功能与平台

| 能力 | Linux | Windows（v0.5.0） |
| --- | --- | --- |
| 面板作为后台服务运行 | `server-panel.service`，低权限账号 | 原生 SCM 服务 `QQONPanel`，虚拟服务账号 |
| 主机指标 | Linux 系统接口，按可用性返回 | 原生 Windows API；不支持项说明原因，不填模拟值 |
| 面板中的系统服务列表 | Linux systemd 范围 | **不支持**；与面板自身注册为 Windows 服务是两回事 |
| Docker 集成 | 显式授权的 Unix socket；默认不开写操作 | **不支持**，没有 named pipe 集成 |
| 访问方式 | 回环地址、SSH 隧道或现有 HTTPS 代理 | 回环地址或现有 HTTPS 代理；远程隧道需自行配置 |

Windows 指标需要特别注意：首个速率样本可以为不可用；CPU 处理器组可能限制统计范围；磁盘指标若采样系统卷，应按标注解释，不把 Linux `/` 或未知磁盘当作真实数据卷。

## 验证不能替代的内容

跨编译只证明能生成目标程序；单元测试和临时目录模拟只覆盖其断言。下列事项仍未完成独立测试机验收：真实 APT/DNF 和 CA、SELinux、文件所有权/ACL、SCM 注册和服务账号、开机启动、停止等待、端口占用、健康检查、升级回退、物理断电与磁盘故障恢复。Linux/Windows CI 成功不能推导所有发行版、架构、桌面与 Server 版本通过。

网站部署、Compose、应用商店、终端、数据库管理、自动证书申请不在当前文档承诺中。安装遇到不在矩阵内的系统应停止并评估，不用“强制”选项掩盖不兼容。
