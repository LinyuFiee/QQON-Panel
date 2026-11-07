# qqon 维护 {#qqon-维护-v0-6-开发中}

**[v0.7.0 预览版已发布](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.7.0)**，提供中文维护菜单与独立 `qqon privilege` 授权。旧安装须先校验、审阅和显式迁移 v0.7.0 管理器，再升级程序；不会因文档更新自动获得新入口。

本页命令适用于匹配的 v0.7.0 安装，普通 yes/update 不代表系统最高权限同意。

## 中文维护入口

安装后运行 `qqon` 打开中文菜单，也可直接使用下表命令。Linux 入口为 `/usr/local/bin/qqon`；Windows 在固定程序目录生成 `qqon.cmd`／`qqon.ps1`，只有经单独确认才追加系统 PATH，随后需重开终端。未追加时使用安装结果给出的完整路径。

| 命令 | 用途与边界 |
| --- | --- |
| `qqon` | 打开中文维护菜单，按提示选择操作 |
| `qqon status` | 只读查看本机服务与安装状态 |
| `qqon url` | 只读显示已配置的访问地址；不证明外网可达 |
| `qqon start` | 启动已安装的面板服务 |
| `qqon stop` | 停止面板服务 |
| `qqon restart` | 重启面板服务，会中断访问 |
| `qqon logs` | 查看有限运行日志；分享前脱敏 |
| `qqon update` | 校验并更新到兼容的官方已发布程序；不自动改为公网访问，不授权最高系统管理权限 |
| `qqon backup` | 先停服并确认进程退出，再生成含敏感 data 的一致备份 |
| `qqon config` | 显式修改访问配置，按事务校验、切换与恢复 |
| `qqon tls renew` | 更新自签证书，按事务保留旧状态并校验切换 |

`status`、`url` 不安装依赖、不启停服务、不修改配置，也不恢复未完成事务。公网 IP 链接仅是配置结果，**外网连通性未检测**；云安全组、NAT 和防火墙仍需管理员配置。维护入口只操作固定面板服务，不是执行任意 root／管理员命令的通用终端。

## 权限与服务身份

安装和维护写操作由 **Linux root** 或 **Windows 管理员终端**执行；终端权限本身不授权面板常驻最高权限。

- 未授权 Linux 使用固定 `server-panel` 账号与 `server-panel.service`；独立同意后服务使用 root。
- 未授权 Windows 使用 `QQONPanel` 与 `NT SERVICE\QQONPanel` 虚拟账号；独立同意后使用 LocalSystem。
- 不因普通升级或修改访问配置而重建账号、接管不明安装，或放宽私有数据权限。

### v0.7.0 独立权限迁移 {#system-management}

`qqon privilege` 是**单独授权入口**；Windows 管理器对应 `-Maintenance privilege`。新安装显式参数为 Linux `--accept-system-management`、Windows `-AcceptSystemManagement`。交互同意须在专用提示后输入 `同意最高权限`，不是泛用“y”。未授权维持 restricted。

授权意味着面板进程本身将以 Linux **root** 或 Windows **LocalSystem** 运行，不是只授权一次命令；失陷可能影响整台主机。必须同时有固定受保护同意记录和正确的实际服务身份，手动把 unit/SCM 改成高权限不算授权。普通 `--yes`／`-Yes`、`qqon update`、配置访问和换证都不授权；网页也不能写同意记录。

已有安装须先校验并审阅 v0.7.0 管理器，显式迁移已知旧管理器，保留原访问和账号。Linux 顺序为 **迁移 manager → `sudo qqon update` 至 v0.7.0、仍为 restricted 并确认健康 → 如需 root 再 `sudo qqon privilege` 独立授权**。

Linux 已确认语法为 `sudo qqon privilege [--accept-system-management] [--yes]`。不带专用标志时走交互同意；带 `--accept-system-management` 明确表达独立授权。**单独 `--yes` 不授权**；普通 `--upgrade`／`--update`／`--status`／`--migrate-manager` 与授权标志组合均拒绝，`qqon update/status` 也不能夹带授权。

Windows 已确认 `qqon privilege` 交互入口，以及 `qqon privilege --accept-system-management --yes` 显式同意入口。未追加 PATH 时，在管理员 PowerShell 中调用已安装的匹配 v0.7 管理器：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "$env:ProgramFiles\QQON-Panel\install.ps1" -Maintenance privilege -AcceptSystemManagement -Yes
```

路径必须保留 PowerShell 双引号以展开环境变量并处理空格；此处 ExecutionPolicy 仅作用于子进程，仍须遵循组织授权。Windows 仅已知 v0.5/v0.6 先独立 Migrate、再 update、最后 privilege；无 Windows v0.4 支持，新装与迁移参数详见 [Windows 命令](./windows-services#privilege-commands)。

v0.7.0 已发布，但发布／阶段 CI 不替代旧安装权限迁移与回退全验收。Windows 本机及阶段 CI 中的 **57 项 TEMP/mock ACL/SCM** 均不是真实 LocalSystem 安装；对应提交、新装与 protocol 5 边界见 [v0.7.0 托管指南](./hosting#verification)。

## 备份与事务

备份前先停止服务并确认进程退出，再复制私有数据，避免运行中写入产生不一致副本。备份包含账号、证书等敏感信息，须保存在受保护位置；不要提交仓库、上传公开网盘或贴入工单。操作按原运行状态恢复服务，原本停止的服务不因备份而启动；结束后仍须确认结果。

`config` 与 `tls renew` 按事务执行：先校验并保留旧配置/证书，再切换与检查；失败按事务记录恢复。不要删除锁、事务或旧文件来强行重试，也不通过忽略 TLS 校验使健康探针成功。磁盘损坏、权限变化和物理断电仍可能阻止恢复，不承诺绝对回退。

## 从旧版迁移到 v0.7.0 {#从-v0-4-v0-5-迁移}

Linux 已知 v0.4/v0.5/v0.6、Windows 已知 v0.5/v0.6 按以下顺序处理；无 Windows v0.4 支持：

1. 备份私有数据，校验和审阅 v0.7.0 管理器，显式迁移已知官方旧管理器，保留原访问、身份和数据。
2. 单独 `qqon update` 升级到 v0.7.0，先确认受限模式下新程序健康。
3. 需要 root／LocalSystem 时独立 `qqon privilege` 同意；普通 yes/update 不能代替。
4. 需要改变公网访问时，再独立 `qqon config`，不自动公开旧服务。

未知或改写的管理器不强制接管，未完成事务先恢复；完整命令见 [升级与迁移](./upgrade-migration)。预览发布和隔离 CI 不等于真实 SCM／逐系统故障恢复验收，见 [支持矩阵](./support-matrix)。

下一步阅读 [公网 IP HTTPS](./public-https) 与 [安全边界](./security)。
