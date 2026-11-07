# qqon 维护 {#qqon-维护-v0-6-开发中}

**[v0.6.0 预览版已发布](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.6.0)**，提供中文维护菜单及以下基础命令。已有 v0.4/v0.5 安装须先显式迁移管理器，不会因文档更新自动获得新入口。

本页命令表针对当前 v0.6.0；`qqon privilege` 属于下方单列的 **v0.7 未发布预览开发接口**，不能在当前下载上假定可用。

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

安装和维护写操作由 **Linux root** 或 **Windows 管理员终端**执行；这不意味着当前 v0.6.0 面板服务以最高权限常驻。

- v0.6.0 Linux 保留固定 `server-panel` 账号与 `server-panel.service`。
- v0.6.0 Windows 保留 `QQONPanel` 服务与 `NT SERVICE\QQONPanel` 虚拟账号。
- 不因普通升级或修改访问配置而重建账号、接管不明安装，或放宽私有数据权限。

### v0.7 独立权限迁移（未发布） {#system-management}

`qqon privilege` 是本轮预览开发中的**单独授权入口**；Windows 管理器对应 `-Maintenance privilege`。新安装显式参数为 Linux `--accept-system-management`、Windows `-AcceptSystemManagement`。交互同意须在专用提示后输入 `同意最高权限`，不是泛用“y”。未授权维持 restricted。

授权意味着面板进程本身将以 Linux **root** 或 Windows **LocalSystem** 运行，不是只授权一次命令；失陷可能影响整台主机。必须同时有固定受保护同意记录和正确的实际服务身份，手动把 unit/SCM 改成高权限不算授权。普通 `--yes`／`-Yes`、`qqon update`、配置访问和换证都不授权；网页也不能写同意记录。

已有安装须先识别已知官方管理器并独立迁移，保留原访问和账号。Linux 开发顺序为 **迁移 manager → `sudo qqon update` 至不低于 v0.7.0、仍为 restricted → `sudo qqon privilege` 独立授权**；先确认新程序在受限身份下健康。

Linux 已确认语法为 `sudo qqon privilege [--accept-system-management] [--yes]`。不带专用标志时走交互同意；带 `--accept-system-management` 明确表达独立授权。**单独 `--yes` 不授权**；普通 `--upgrade`／`--update`／`--status`／`--migrate-manager` 与授权标志组合均拒绝，`qqon update/status` 也不能夹带授权。

Windows 已确认 `qqon privilege` 交互入口，以及 `qqon privilege --accept-system-management --yes` 显式同意入口。未追加 PATH 时，在管理员 PowerShell 中调用已安装的匹配 v0.7 管理器：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "$env:ProgramFiles\QQON-Panel\install.ps1" -Maintenance privilege -AcceptSystemManagement -Yes
```

路径必须保留 PowerShell 双引号以展开环境变量并处理空格；此处 ExecutionPolicy 仅作用于子进程，仍须遵循组织授权。Windows 仅已知 v0.5/v0.6 先独立 Migrate、再 update、最后 privilege；无 Windows v0.4 支持，新装与迁移参数详见 [Windows 命令](./windows-services#privilege-commands)。

当前没有公开 v0.7 发行可升级，v0.6 只能受限安装。最新合并 CI 的 Linux unit／race／真实托管和 Windows Go＋57 项 PowerShell 均已成功，但不能替代旧安装权限迁移／回退全验收；Windows 本机及 CI 中的 **57 项 TEMP/mock ACL/SCM** 均不是真实 LocalSystem 安装。对应提交与验证范围、Linux 新装示例及 protocol 5 边界见 [v0.7 托管开发指南](./hosting#verification)。

## 备份与事务

备份前先停止服务并确认进程退出，再复制私有数据，避免运行中写入产生不一致副本。备份包含账号、证书等敏感信息，须保存在受保护位置；不要提交仓库、上传公开网盘或贴入工单。操作按原运行状态恢复服务，原本停止的服务不因备份而启动；结束后仍须确认结果。

`config` 与 `tls renew` 按事务执行：先校验并保留旧配置/证书，再切换与检查；失败按事务记录恢复。不要删除锁、事务或旧文件来强行重试，也不通过忽略 TLS 校验使健康探针成功。磁盘损坏、权限变化和物理断电仍可能阻止恢复，不承诺绝对回退。

## 从 v0.4 / v0.5 迁移

已有受管理安装按以下顺序升级 v0.6.0：

1. 备份私有数据，识别官方旧管理器，经管理员**显式确认**迁移新管理器；保留旧访问方式、端口、服务身份和数据。
2. 单独升级到支持新配置的程序，先确认新程序在原访问方式下正常运行。
3. 如需公网 IP HTTPS，再通过 `qqon config` 确认公网 IPv4 和访问配置；不能先把新公网配置交给旧程序。

迁移或普通升级不会因新安装默认值变化而自动开放公网。未知手动安装、被修改的管理器不能强制接管；完整边界见 [升级与迁移](./upgrade-migration)。

Windows 新增 maintenance 隔离测试已在 [CI #42349315659](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42349315659) 通过，不代表宿主真实 SCM 服务安装或各系统验收完成，见 [支持矩阵](./support-matrix)。

下一步阅读 [公网 IP HTTPS](./public-https) 与 [安全边界](./security)。
