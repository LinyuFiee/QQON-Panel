# 安装说明

**[v0.6.0 预览版已发布](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.6.0)**，提供 Linux APT/DNF 与 Windows x64 中文安装、四种访问模式及 `qqon` 维护。直接开始见 [一键命令](./quickstart#next-release)；旧版见 [v0.4.0 指南](./legacy-v0-4) 与 [v0.5.0 说明](./releases#下一版本-v0-5-待发布)。

本页是**当前 v0.6.0 下载**的安装说明。v0.4.0／v0.5.0／v0.6.0 均为预览定位，没有真实业务软件安装和建站能力。v0.7 的 root／LocalSystem 授权与 Linux 托管尚在开发、未发布、测试未定，另见 [托管开发指南](./hosting)；不要给当前下载附加 v0.7 专属参数。

Linux/Windows CI（含 Windows 新增 maintenance 测试）已通过，但不等于具体系统版本安装、真实 SCM、SELinux 或物理故障恢复已验收，详见 [支持矩阵](./support-matrix)。

## 下载与信任

首页命令下载的是 GitHub Pages 上的薄入口，而不是另一份独立维护的安装器：

- `install.sh` 来自仓库 `scripts/bootstrap.sh`。
- `install.ps1` 来自仓库 `scripts/bootstrap.ps1`。
- 文档构建时按字节复制；源文件不存在会让构建失败，不生成占位可执行文件。

入口固定面向 `LinyuFiee/QQON-Panel` 的官方已发布 Release，必须拒绝旧的、不兼容的发布格式。官方来源不等于成熟稳定；不从可变镜像、用户传入的任意 URL 或 `main` 源码悄悄替换已发布安装器。

**管道命令会直接执行下载内容。** GitHub Pages、项目仓库及其发布权限都是信任边界；SHA-256 校验只能证明文件与清单一致，不能提供独立发布者签名。更谨慎的做法是下载到新的空目录，审阅后执行本地文件，并先确认 [发布状态](./releases)。

### Linux 先下载审阅

在有 `curl` 的交互式 shell 中下载；审阅完成前不要执行安装器：

```sh
entry_dir="$(mktemp -d)"
curl -fL --proto '=https' --proto-redir '=https' --max-time 120 https://linyufiee.github.io/QQON-Panel/install.sh -o "$entry_dir/install.sh"
less "$entry_dir/install.sh"
```

确认后以 root 执行这份已审阅文件（已经是 root 时省略 `sudo`）：

```sh
sudo bash "$entry_dir/install.sh"
```

若仅有 `wget`，可用：

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

## 四种访问模式

| 模式 | 行为 |
| --- | --- |
| 公网 IP 自签 HTTPS（`public-selfsigned`，默认） | 用户确认公网 IPv4，自签 HTTPS 默认 `8443`，核对浏览器证书指纹 |
| 仅本机 | 回环监听，通过本机或自行配置的 SSH 隧道访问 |
| 已有 HTTPS 代理 | 使用管理员已有的独立 HTTPS Origin，代理负责 TLS |
| 导入证书 | 使用管理员准备、与访问地址匹配的证书和私钥 |

新安装完成后输出配置 URL、端口、服务状态、证书指纹／有效期和私有初始化令牌文件位置。自签证书有浏览器警告，须核对可信终端的 SHA-256 指纹。**公网链接仅表示配置结果，外网连通性未检测**；云安全组、主机防火墙和 NAT 由管理员配置。DNS、代理、公共 CA 证书申请也不由安装器自动完成，见 [公网 HTTPS](./public-https)。

旧 v0.4/v0.5 保留回环／代理行为，不自动套用新装默认值；先迁移管理器、再升级程序，最后按需运行 `qqon config`。

## Linux 安装流程 {#linux-installation}

中文菜单提供安装、升级与只读状态；首次检查发行版、root、systemd 和架构，确认写操作后再补齐所缺依赖。

- Debian/Ubuntu 使用现有 APT 源；Rocky/AlmaLinux 使用现有 DNF 源。
- 最小依赖为 Python 3、CA 证书以及确实缺失的账号/系统工具；不会安装 Go、Node、数据库或 Docker。
- 按上述四种模式选择访问方式；公网默认 HTTPS `8443`，不开放裸 HTTP 公网。
- 使用固定低权限服务账号。完成后检查安装记录、unit、实际监听与服务版本，而不是仅看到“下载完成”。

Linux 既定路径如下，升级/迁移不得随意接管未知同名文件：

| 用途 | 路径 / 名称 |
| --- | --- |
| 程序 | `/usr/local/bin/panel` |
| 维护入口 | `/usr/local/bin/qqon` |
| 兼容更新入口 | `/usr/local/bin/panel-update` |
| 已审阅本地管理器 | `/usr/local/lib/server-panel/install.sh` |
| 安装记录 | `/etc/server-panel/install.json` |
| 私有数据 | `/var/lib/server-panel` |
| 账号与 systemd unit | `server-panel` / `server-panel.service` |

## Windows 安装流程 {#windows-安装流程-待发布}

管理员可交互安装或使用明确参数，使用原生 PowerShell/.NET，不引入 Python/WSL/Node。四种模式与公网自签默认值同上。

程序作为原生 SCM 服务 `QQONPanel` 运行，虚拟服务账号为 `NT SERVICE\QQONPanel`，不是把任意控制台程序包装成假服务。NTFS 权限、受保护目录和服务停止等待失败时应停止，不用删除锁文件或放宽 ACL 继续。

默认程序位于系统 Program Files 的 `QQON-Panel\panel.exe`，私有数据位于系统 ProgramData 的 `QQON-Panel\data`；不假设系统盘一定是 `C:`。`qqon.cmd`／`qqon.ps1` 位于固定程序目录，单独确认追加 PATH 后需重开终端；不追加时按安装结果的完整路径运行。详细权限和只读检查见 [Windows 服务](./windows-services)。

## 本机与 SSH 隧道 {#local-access}

仅本机模式以及旧 v0.4/v0.5 回环安装可使用 SSH 隧道；旧版默认地址为 `127.0.0.1:5173`。在自己的电脑执行并替换实际用户和主机：

```sh
ssh -L 5173:127.0.0.1:5173 your-user@your-server
```

随后访问 `http://127.0.0.1:5173/`。使用其他端口时按实际监听同步调整；Windows 远程 SSH 服务需管理员自行配置。已有 HTTPS 代理模式通过配置的 HTTPS Origin 访问，不关闭 Host/Origin 检查。

## 无人值守与状态

无人值守仅使用**已发布且审阅过的安装器帮助**中的明确参数，不混用 v0.4/v0.5/v0.6 的参数与配置。`--yes` / `-Yes` 不代表自动批准旧管理器迁移或绕过公网地址确认、安全校验，也**不构成 v0.7 的最高系统管理权限同意**；`qqon update` 同样不授权。

没有交互终端的管道不会从网络输入流读答案。安装后的 `qqon status`、`qqon url` 只读，不安装依赖、迁移记录、恢复事务、改权限或编辑服务；其他维护能力见 [qqon 维护](./maintenance)。

安装中断或看到版本/摘要/目录权限不一致时，保留错误和事务记录；按 [故障排查](./troubleshooting) 处理，不重复强制安装覆盖数据。
