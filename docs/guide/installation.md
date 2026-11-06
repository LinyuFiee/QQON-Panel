# 安装说明

::: warning v0.5 待发布
本页交互安装、RPM 和 Windows 路径属于下一版本。当前 v0.4.0 的六个附件不能满足新入口的格式要求，**不要期待首页命令现在完成安装**。需要现成产物请使用 [v0.4.0 Linux 指南](./legacy-v0-4)。
:::

## 下载与信任

首页命令下载的是 GitHub Pages 上的薄入口，而不是另一份独立维护的安装器：

- `install.sh` 来自仓库 `scripts/bootstrap.sh`。
- `install.ps1` 来自仓库 `scripts/bootstrap.ps1`。
- 文档构建时按字节复制；源文件不存在会让构建失败，不生成占位可执行文件。

入口固定面向 `LinyuFiee/QQON-Panel` 的正式 Release，必须拒绝旧的、不兼容的发布格式。不从可变镜像、用户传入的任意 URL 或 `main` 源码悄悄替换生产安装器。

**管道命令会直接执行下载内容。** GitHub Pages、项目仓库及其发布权限都是信任边界；SHA-256 校验只能证明文件与清单一致，不能提供独立发布者签名。更谨慎的做法是下载到新的空目录，审阅后执行本地文件，并先确认 [发布状态](./releases)。

### Linux 先下载审阅

在有 `curl` 的交互式 shell 中下载；审阅完成前不要执行最后一条命令：

```sh
entry_dir="$(mktemp -d)"
curl -fL --proto '=https' --proto-redir '=https' --max-time 120 https://linyufiee.github.io/QQON-Panel/install.sh -o "$entry_dir/install.sh"
less "$entry_dir/install.sh"
```

确认后以 root 执行这份已审阅文件（已经是 root 时省略 `sudo`）：

```sh
sudo bash "$entry_dir/install.sh"
```

若仅有 `wget`，下一版本可用的简短入口为：

```sh
wget -qO- https://linyufiee.github.io/QQON-Panel/install.sh | bash
```

这条管道同样需要 root 的交互式 Bash、有效证书与真实终端。两种下载工具都没有时，先用系统渠道准备其中之一，或在可信电脑下载、审阅并通过 SFTP 传入新目录；不要关闭 TLS 校验。

### Windows 先下载审阅

在管理员 Windows PowerShell 5.1 x64 中创建独立目录并下载：

```powershell
$entryDir = Join-Path ([IO.Path]::GetTempPath()) ('qqon-review-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $entryDir -ErrorAction Stop | Out-Null
$entryFile = Join-Path $entryDir 'install.ps1'
Invoke-WebRequest -UseBasicParsing 'https://linyufiee.github.io/QQON-Panel/install.ps1' -OutFile $entryFile
notepad.exe $entryFile
```

关闭编辑器并确认内容后执行本地文件：

```powershell
& $entryFile
```

如果组织执行策略或安全软件阻止运行，应按组织批准的脚本签名/执行流程处理；本文不建议改变全局 ExecutionPolicy 或停用安全软件。`irm | iex` 也不是绕过组织安全要求的理由。

## Linux 安装流程（待发布） {#linux-installation}

目标菜单为安装、升级、只读状态和退出。首次安装前检查发行版、root、systemd 和架构；确认写操作后再补齐所缺依赖。

- Debian/Ubuntu 使用现有 APT 源；Rocky/AlmaLinux 使用现有 DNF 源。
- 最小依赖为 Python 3、CA 证书以及确实缺失的账号/系统工具；不会安装 Go、Node、数据库或 Docker。
- 首次选择 `1024–65535` 端口，默认 `5173`。监听固定为 `127.0.0.1`，不是开放公网 HTTP。
- 选择本地/SSH 或已经存在的 HTTPS 代理。Origin 使用明确的 HTTPS 域名，不能含任意路径或凭据；域名、TLS、代理和防火墙由管理员配置。
- 脚本执行后应检查安装记录、unit、监听地址及实际服务版本一致，而不是仅看到“下载完成”。

Linux 既定路径如下，升级/迁移不得随意接管未知同名文件：

| 用途 | 路径 / 名称 |
| --- | --- |
| 程序 | `/usr/local/bin/panel` |
| 更新入口 | `/usr/local/bin/panel-update` |
| 已审阅本地管理器 | `/usr/local/lib/server-panel/install.sh` |
| 安装记录 | `/etc/server-panel/install.json` |
| 私有数据 | `/var/lib/server-panel` |
| 账号与 systemd unit | `server-panel` / `server-panel.service` |

## Windows 安装流程（待发布）

目标为管理员交互安装或明确参数操作，使用原生 PowerShell/.NET，不引入 Python/WSL/Node。首次端口和 Origin 的安全默认值与 Linux 相同。

程序作为原生 SCM 服务 `QQONPanel` 运行，虚拟服务账号为 `NT SERVICE\QQONPanel`，不是把任意控制台程序包装成假服务。NTFS 权限、受保护目录和服务停止等待失败时应停止，不用删除锁文件或放宽 ACL 继续。

默认程序位于系统 Program Files 的 `QQON-Panel\panel.exe`，私有数据位于系统 ProgramData 的 `QQON-Panel\data`；不假设系统盘一定是 `C:`。详细名称、权限和只读检查见 [Windows 服务](./windows-services)。

## 无人值守与状态

下一版本约定 Linux 保留明确的 `--version`、`--yes` 等参数并提供只读状态；Windows 提供 `-Action Install|Update|Status`、`-Port`、`-Origin`、`-Yes`。**以已发布且审阅过的安装器帮助为准**，不要把这些参数传给 v0.4.0 后假设生效。

没有交互终端的管道不会从网络输入流读答案，应改用受审阅的本地脚本与明确参数。只读状态不应安装依赖、迁移记录、恢复事务、改权限或编辑服务。

安装中断或看到版本/摘要/目录权限不一致时，保留错误和事务记录；按 [故障排查](./troubleshooting) 处理，不重复强制安装覆盖数据。
