# Windows 服务

::: info v0.6.0 已发布 · 真实 SCM 安装仍未验证
[v0.6.0 正式 Release](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.6.0) 已提供 Windows x64 程序、中文安装器与维护入口；[CI #42349315659](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42349315659) 的 `windows-test` 含新增 maintenance 隔离测试已成功。**这不代表宿主真实 SCM/虚拟账号/ACL 或 Windows 10/11、各 Server 版本已完成安装、升级与故障恢复验收。** 请使用正式安装器，不手工拼装服务代替安装流程。
:::

## 运行方式

程序实现原生 Windows Service Control Manager（SCM）服务模式。服务接收启动、停止和系统关机事件，启动到监听就绪才报告运行，停止时等待程序清理。交互式控制台路径与服务路径共享应用启动逻辑，但普通控制台进程不等于 Windows 服务。

| 项目 | 约定值 |
| --- | --- |
| SCM 服务名 | `QQONPanel` |
| 显示名称 | `QQON-Panel` |
| 服务身份 | `NT SERVICE\QQONPanel`，不保存账号密码 |
| 新装访问 | 默认公网 IPv4 自签 HTTPS `8443`；也可选择仅本机、已有 HTTPS 代理或导入证书 |
| 旧版访问 | v0.5 默认 `127.0.0.1:5173`，可选端口；迁移保留实际旧配置 |
| 平台 | Windows PowerShell 5.1 x64、管理员安装、NTFS |

面板自身能够作为 Windows 服务运行，**不表示网页中的系统服务列表支持 Windows**。Windows Docker named pipe 集成也不在本轮范围内。

## 程序与数据目录

默认位置如下；实际系统目录由 Windows 环境解析，不硬编码 `C:` 系统盘：

| 用途 | 默认示例 | 权限边界 |
| --- | --- | --- |
| 程序 | `C:\Program Files\QQON-Panel\panel.exe` | 管理员/SYSTEM 可修改；服务身份仅读取执行 |
| 私有数据 | `C:\ProgramData\QQON-Panel\data` | 管理员/SYSTEM 与服务身份访问，受保护并可安全继承的 ACL |
| 安装记录 | `C:\ProgramData\QQON-Panel\install.json` | 管理员管理，不由面板网页改写 |
| 升级事务 | 系统 ProgramData 下独立的受保护事务记录 | 与私有账号数据分离，不自行删除 |

安装器必须检查已存在的路径、重解析点和权限。不明旧目录、广泛 ACL 或不安全所有者不是可以静默“修好后接管”的安装路径。不要递归删除 ProgramData 来重新安装。

## 安装与只读检查

使用 v0.6.0 的 [受审阅安装入口](./installation)。安装完成后可用 `qqon status`、`qqon url` 或 Windows 自带的只读命令确认服务；在尚未安装的机器上看到“不存在”并不说明程序崩溃。

```powershell
Get-Service -Name QQONPanel
sc.exe qc QQONPanel
sc.exe queryex QQONPanel
```

使用 `sc.exe` 而不是 PowerShell 中可能指向其他命令的 `sc`。检查名称、服务账号和程序路径是否与本次安装一致。初始令牌位置应以安装器和实际本机输出为准，不假设 Linux 的 `journalctl` 可用，也不要将私有令牌发到工单。

新装默认公网自签 HTTPS，需确认公网 IPv4 并核对 SHA-256 证书指纹；浏览器信任警告是预期行为。配置 URL **未检测外网可达性**，云安全组、防火墙和 NAT 需人工配置。旧 v0.5 或仅本机模式继续按回环／代理配置访问；见 [公网 HTTPS](./public-https)。

## 管理与停止

安装器在固定程序目录生成 `qqon.cmd`／`qqon.ps1`，单独确认后才追加系统 PATH，需重开终端；拒绝追加则使用安装输出给出的完整路径。`qqon` 打开中文菜单，`qqon start`、`qqon stop`、`qqon restart` 只操作固定面板服务，不是任意管理员命令入口。

也可在**安装成功之后**由管理员按需执行：

```powershell
Stop-Service -Name QQONPanel
Start-Service -Name QQONPanel
```

停止后应确认服务与对应进程确实退出，才能备份、切换或手动处理程序。不要通过删除锁文件、反复覆盖运行中的 `panel.exe`、切换为高权限账户来绕过等待或 ACL 错误。`qqon backup` 的一致备份含敏感数据，配置与 `qqon tls renew` 采用事务，详见 [维护指南](./maintenance)。

## 更新与回退边界

升级器先获取互斥锁、下载并验证唯一 `panel.exe`、在程序所在文件系统暂存并备份；停服务后等待进程退出与文件解锁，再原子替换、启动并核对健康状态和版本。

旧 v0.5 先显式迁移新管理器并保留原访问，再升级新程序，最后按需运行 `qqon config`，不会自动公开旧服务。

失败回退必须先停止新版并等待退出，再恢复旧程序；**不会回滚或重置私有账号数据**。遇到超时、服务未退出、文件被锁或回退失败，保留事务记录并阅读 [故障排查](./troubleshooting#windows-errors)，不要“解锁后强拷贝”。

## 验证范围

Windows CI 与新增 maintenance 隔离测试已通过，但临时目录模拟、ZIP/JSON 解析和 runner 单元测试不能证明真实 SCM/虚拟账号/Program Files ACL 已通过。具体桌面/Server 版本的真实安装、重启、权限、升级、回退、异常停止和物理故障恢复仍未验证，需专用测试机验收；完整范围见 [支持矩阵](./support-matrix)。
