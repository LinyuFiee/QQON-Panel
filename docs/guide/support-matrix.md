# 支持矩阵与验证状态

::: warning “实现了”不等于“验证过”
当前正式 Release 是 **v0.4.0**。v0.5 代码集成、CI 构建和真实系统安装是三个不同阶段；下表不以跨平台编译成功代替实机验收，也不把 Windows/RPM 目标列成全面支持。
:::

## 系统范围

| 系统 / 架构 | 实现与发行状态 | CI 验证边界 | 实机状态 |
| --- | --- | --- | --- |
| Debian 12、13 / amd64、arm64 | v0.4.0 已发布 Linux 安装路径；APT、systemd | 已有 Linux 测试/发行构建配置；不是每个 Debian 版本的安装验证 | 本页不提供逐版本、逐架构已验收声明 |
| Ubuntu 22.04、24.04 / amd64、arm64 | v0.4.0 已发布 Linux 安装路径；APT、systemd | Linux runner 测试不等于两版本全套升级恢复验收 | 本页不提供逐版本、逐架构已验收声明 |
| Rocky Linux 9、10 / amd64、arm64 | v0.5 DNF 适配目标，待发布 | 临时目录、模拟包管理器与编译不能验证真实 DNF/SELinux/systemd | **未完成全部实机验证** |
| AlmaLinux 9、10 / amd64、arm64 | v0.5 DNF 适配目标，待发布 | 与 Rocky 相同；不能用同系发行版测试替代 | **未完成全部实机验证** |
| Windows 10、11 / x64 | v0.5 原生运行、指标及服务安装路径，待发布 | Windows 测试/构建须以对应提交的实际 CI 结果为准；不宣称本页已验证 | **未完成全部实机安装、升级、回退验证** |
| Windows Server 2019、2022、2025 / x64 | v0.5 目标，待发布 | 单个 Windows runner 不能覆盖三个 Server 版本 | **未完成全部实机验证** |

v0.5 的“实现目标/待发布”描述源码工作范围，不表示安装包已经可下载。是否已完成某项 CI 验证，请查看 [对应仓库运行记录](https://github.com/LinyuFiee/QQON-Panel/actions) 的**具体提交和测试项**；此页没有把尚未核验的 run 标为通过。

## 必须满足的条件

### Linux

- root 安装权限、Bash、正在运行的 systemd；架构为 amd64 或 arm64。
- 能访问 GitHub HTTPS、有效 CA 证书、发行版现有软件源正常。
- 安装器所需 Python 至少 3.8；只补齐缺失的最小依赖。Linux 一行入口初始仍需要 `curl` 或 `wget`。
- 不支持仅有 BusyBox、没有活动 systemd 的容器；不会通过强装 systemd 来“支持”它们。
- 不自动升级整机、修改软件源、关闭 SELinux、开防火墙端口或安装 Docker。

### Windows（v0.5 待发布）

- x64 Windows，管理员 Windows PowerShell 5.1、NTFS、本机服务控制管理器（SCM）。
- 使用系统 PowerShell/.NET 的 HTTPS、ZIP 和 SHA-256 能力；用户机器不需 Python、WSL、Go 或 Node。
- ARM64、32 位 Windows、非 NTFS、Wine 不在本轮安装范围内。不能靠关闭证书校验、全局执行策略或 ACL 检查绕过限制。

## 功能与平台

| 能力 | Linux | Windows（v0.5 待发布） |
| --- | --- | --- |
| 面板作为后台服务运行 | `server-panel.service`，低权限账号 | 原生 SCM 服务 `QQONPanel`，虚拟服务账号 |
| 主机指标 | Linux 系统接口，按可用性返回 | 原生 Windows API；不支持项说明原因，不填模拟值 |
| 面板中的系统服务列表 | Linux systemd 范围 | **不支持**；与面板自身注册为 Windows 服务是两回事 |
| Docker 集成 | 显式授权的 Unix socket；默认不开写操作 | **不支持**，没有 named pipe 集成 |
| 访问方式 | 回环地址、SSH 隧道或现有 HTTPS 代理 | 回环地址或现有 HTTPS 代理；远程隧道需自行配置 |

Windows 指标需要特别注意：首个速率样本可以为不可用；CPU 处理器组可能限制统计范围；磁盘指标若采样系统卷，应按标注解释，不把 Linux `/` 或未知磁盘当作真实数据卷。

## 验证不能替代的内容

跨编译只证明能生成目标程序；单元测试和临时目录模拟只覆盖其断言。下列事项仍需独立测试机验证：真实包管理器和 CA、文件所有权/ACL、服务账号、开机启动、停止等待、端口占用、健康检查、升级回退与断电恢复。Windows CI 成功也不能推导所有桌面与 Server 版本通过。

网站部署、Compose、应用商店、终端、数据库管理、自动证书申请不在当前文档承诺中。安装遇到不在矩阵内的系统应停止并评估，不用“强制”选项掩盖不兼容。
