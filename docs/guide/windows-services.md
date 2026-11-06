# Windows 服务

::: warning v0.5 待发布 · 未全面实机验证
当前 v0.4.0 没有 Windows Release 附件。本页描述下一版本的原生服务安装约束，**不代表 Windows 10/11 或各 Server 版本已经完成真实安装、升级与故障恢复验收**。不要手工拼装服务去替代未发布的安装流程。
:::

## 运行方式

下一版本程序通过 Windows Service Control Manager（SCM）运行。服务接收启动、停止和系统关机事件，启动到监听就绪才报告运行，停止时等待程序清理。交互式控制台路径与服务路径共享应用启动逻辑，但普通控制台进程不等于 Windows 服务。

| 项目 | 约定值 |
| --- | --- |
| SCM 服务名 | `QQONPanel` |
| 显示名称 | `QQON-Panel` |
| 服务身份 | `NT SERVICE\QQONPanel`，不保存账号密码 |
| 监听 | `127.0.0.1:5173`；首次安装可选 `1024–65535` |
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

兼容 Release 发布后，使用 [受审阅的安装入口](./installation)。安装完成后可用 Windows 自带的只读命令确认服务；在尚未安装的机器上看到“不存在”并不说明程序崩溃。

```powershell
Get-Service -Name QQONPanel
sc.exe qc QQONPanel
sc.exe queryex QQONPanel
```

使用 `sc.exe` 而不是 PowerShell 中可能指向其他命令的 `sc`。检查名称、服务账号和程序路径是否与本次安装一致。初始令牌位置应以安装器和实际本机输出为准，不假设 Linux 的 `journalctl` 可用，也不要将私有令牌发到工单。

本地默认从 `http://127.0.0.1:5173/` 访问。远程访问需已有 HTTPS 代理或自行配置的隧道；安装器不自动开放公网 HTTP、配置 SSH 服务或添加防火墙规则。

## 管理与停止

以下是**安装成功之后**管理员按需执行的服务操作，不是开发测试步骤，也不会代替安全安装流程：

```powershell
Stop-Service -Name QQONPanel
Start-Service -Name QQONPanel
```

停止后应确认服务与对应进程确实退出，才能备份、切换或手动处理程序。不要通过删除锁文件、反复覆盖运行中的 `panel.exe`、切换为高权限账户来绕过等待或 ACL 错误。

## 更新与回退边界

下一版本升级器应获取互斥锁、下载并验证唯一 `panel.exe`、在程序所在文件系统暂存并备份；停服务后等待进程退出与文件解锁，再原子替换、启动并核对健康状态和版本。

失败回退必须先停止新版并等待退出，再恢复旧程序；**不会回滚或重置私有账号数据**。遇到超时、服务未退出、文件被锁或回退失败，保留事务记录并阅读 [故障排查](./troubleshooting#windows-errors)，不要“解锁后强拷贝”。

## 验证范围

临时目录模拟、ZIP/JSON 解析测试和 Windows runner 的单元测试不能证明真实 SCM/虚拟账号/Program Files ACL 已通过，也不能覆盖所有目标系统。对应正式 Release 发布前仍需专用测试机验证安装、重启、权限、升级、回退与异常停止；完整范围见 [支持矩阵](./support-matrix)。
