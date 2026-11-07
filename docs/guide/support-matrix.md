# 支持矩阵与验证状态

::: warning “已发布 / CI 通过”不等于“实机验收”
当前已发布预览版是 **[v0.6.0](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.6.0)**；v0.4／v0.5／v0.6 均不是稳定成熟承诺。[发布 CI #42349315659](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42349315659) 的 `test-build`、`windows-test`、`publish` 均为 `success`，含新增 Windows maintenance 隔离测试。发布、CI 验证和具体系统安装是不同层次；不能据此宣称所有系统或真实 SCM 安装已完成验收。**v0.7 预览尚未发布；其 Ubuntu 真实托管 CI 与 Windows 本机隔离结果在下节单列，不沿用旧发布结果作本轮验收。**
:::

## 系统范围

| 系统 / 架构 | 实现与发行状态 | CI 验证边界 | 实机状态 |
| --- | --- | --- | --- |
| Debian 12、13 / amd64、arm64 | v0.6.0 Linux 产物与 APT、systemd 安装路径已发布 | `test-build` 成功；不等于逐版本 APT/systemd 安装验收 | **具体版本、架构安装与恢复仍未验证** |
| Ubuntu 22.04、24.04 / amd64、arm64 | v0.6.0 Linux 产物与 APT、systemd 安装路径已发布 | `test-build` 成功；Linux runner 不覆盖两个版本的全套安装/升级 | **具体版本、架构安装与恢复仍未验证** |
| Rocky Linux 9、10 / amd64、arm64 | v0.6.0 Linux 产物与 DNF 适配已发布 | `test-build` 成功；模拟包管理器不验证真实 DNF/SELinux/systemd | **具体版本安装、SELinux 与恢复仍未验证** |
| AlmaLinux 9、10 / amd64、arm64 | v0.6.0 Linux 产物与 DNF 适配已发布 | `test-build` 成功；不能用 Rocky 或同系测试替代 | **具体版本安装、SELinux 与恢复仍未验证** |
| Windows 10、11 / x64 | v0.6.0 原生程序、指标与服务安装器已发布 | `windows-test` 含 maintenance 隔离测试成功；不是实际 SCM 注册 | **真实 SCM、ACL、安装/升级/回退仍未验证** |
| Windows Server 2019、2022、2025 / x64 | v0.6.0 x64 产物与服务安装器已发布 | `windows-test` 成功；单个 runner 不覆盖三个 Server 版本 | **逐版本真实安装、权限与恢复仍未验证** |

上述 CI 对应已发布 tag 提交 `3cfd1a8fe75eca46fd1d991749c1be39f546638c`。新增 Windows 维护测试使用隔离环境，不安装宿主真实服务。旧 v0.5 的附件复核和只读版本检查保留在 [历史发布记录](./releases#下一版本-v0-5-待发布)，不能代替 v0.6 的真实系统验收。

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

| 能力 | Linux | Windows（v0.6.0） |
| --- | --- | --- |
| 面板作为后台服务运行 | `server-panel.service`，低权限账号 | 原生 SCM 服务 `QQONPanel`，虚拟服务账号 |
| 主机指标 | Linux 系统接口，按可用性返回 | 原生 Windows API；不支持项说明原因，不填模拟值 |
| 面板中的系统服务列表 | Linux systemd 范围 | **不支持**；与面板自身注册为 Windows 服务是两回事 |
| Docker 集成 | 显式授权的 Unix socket；默认不开写操作 | **不支持**，没有 named pipe 集成 |
| 访问方式 | 公网自签 HTTPS、仅本机、已有 HTTPS 代理、导入证书 | 同样四种模式；远程隧道需自行配置 |
| 维护入口 | `qqon` 固定面板维护 | `qqon.cmd`／`qqon.ps1` 固定面板维护 |

新装默认公网 IPv4 自签 HTTPS `8443`，需要用户确认地址并核对证书指纹。公网 URL 仅为配置结果，**外网连通性未检测**；云安全组、防火墙、NAT 需人工配置。旧 v0.4/v0.5 迁移保留原回环／代理访问，见 [公网 HTTPS](./public-https) 与 [维护指南](./maintenance)。

Windows 指标需要特别注意：首个速率样本可以为不可用；CPU 处理器组可能限制统计范围；磁盘指标若采样系统卷，应按标注解释，不把 Linux `/` 或未知磁盘当作真实数据卷。

## 验证不能替代的内容

跨编译只证明能生成目标程序；单元测试和临时目录模拟只覆盖其断言。上表记录的是 v0.6 历史发行范围；下节新增的 v0.7 Ubuntu 真实托管 CI 也只覆盖列出的场景。逐发行版／架构安装、DNF、SELinux、Windows 真实 SCM/ACL、开机启动、完整旧机迁移／权限回退及物理断电／磁盘故障仍不能由单个 runner 推导通过。

## v0.7 托管预览开发范围 {#hosting-preview}

**未发布；已有有限验证，不代表全平台验收。** 下表是本轮开发能力与明确限制，不是已发布支持承诺；实际操作见 [托管开发指南](./hosting)，已完成与未覆盖的测试在表后单列。

| 能力 | Linux 开发范围 | Windows 开发范围 |
| --- | --- | --- |
| 最高权限服务身份 | 独立授权后 root；未授权 restricted | 独立授权后 LocalSystem；未授权虚拟服务账号 |
| 真实软件安装／启停 | 白名单 Nginx、PHP-FPM、MariaDB、Redis；现有可信 APT/DNF 源和 systemd | **不支持 Linux 软件管理**，LocalSystem 不改变平台限制 |
| 静态／代理／PHP 站点 | 受管根、Nginx 配置验证、reload、本机站点标记检查 | **不支持 Linux Nginx 建站** |
| 网站文件与 TLS | 单文件上传 ≤2 MiB、UTF-8 编辑 ≤256 KiB；已有 PEM 证书导入，非 ACME | 随上述网站能力一并不支持 |
| 任务结果 | 有界共享队列、进度与日志；真实失败／中断可见 | 不把平台不支持请求伪装成成功任务 |

最高权限需 `--accept-system-management`／`-AcceptSystemManagement` 或新 `qqon privilege` 的独立同意；普通 yes/update 不会授权。

### v0.7 本轮验证结果

最新全合并代码 [CI #42380757509](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42380757509) 已成功，提交 `fa1483f9ec57294d000a6f71014c4e6ee7afe87c`；Linux unit／race／真实托管及 Windows Go＋57 项 PowerShell 全部成功，但 Windows 仍非真实 SCM／LocalSystem 安装。下表的 `80fea3b…` 场景与 15.42 MiB 属于前一次独立记录。

| 环境／项目 | 已确认结果 | 剩余边界 |
| --- | --- | --- |
| Ubuntu 真实托管 CI | [run #42379665605](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42379665605) 成功；提交 `80fea3b505bc429541ba31ed2f1ac751debaaf9e`。实际缺包安装并启动 Nginx `1.24.0-2ubuntu7.19`／PHP `8.3.6-0ubuntu0.24.04.13`，验证静态／代理／PHP HTTP、上传编辑、站点停用／启用、损坏 Nginx fixture 回退与重启、删除保留文件、认证／CSRF／同意撤销 | 不外推到其他发行版／架构、完整旧机迁移或真实 Nginx HTTPS |
| Windows PowerShell 本机及后续 CI | 本机 **57/57**：install 23、maintenance 18、privilege 16；同组 57 项随后在最新合并 CI 成功，仍为 TEMP/mock ACL/SCM | 不是真实 LocalSystem／SCM 安装验收 |
| MariaDB／Redis | 仅 fake 逻辑测试 | **未真实安装**，不以 Nginx/PHP 结果替代 |
| DNF | 仅开发／模拟覆盖 | **未真实测试**；Rocky／Alma／SELinux 仍须独立验收 |
| 网站 HTTPS 导入 | 所有站点域名校验；单元／loopback 测试 | **真实 Nginx HTTPS 未验证**，不是 ACME |
| GUI | 仅登录截图通过 | 后续截图工具超时，完整 GUI 验收未完成 |
| 面板 RSS | `80fea3b…` 对应 Ubuntu CI 中 5 次操作后短时采样，中位数＝最大值＝**15.42 MiB** | 不移作后续合并 CI 测量；仅面板进程，不包含业务 RAM，不是 SLA、soak 或完整负载测试 |

完整验证细节见 [托管开发指南](./hosting#verification)。单个 Ubuntu runner 成功不能推导 Windows 实际服务安装、全发行版、长期负载、权限迁移／回退与物理故障恢复全部通过。

v0.4.0／v0.5.0／v0.6.0 没有真实软件安装和网站部署；v0.7 开发范围也不包括 Compose、完整应用商店、终端、数据库管理或 ACME。安装遇到不在矩阵内的系统应停止并评估，不用“强制”选项掩盖不兼容。
