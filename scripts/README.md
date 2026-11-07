# 一键安装与维护

完整中文文档：**https://linyufiee.github.io/QQON-Panel/**

> **[v0.6.0 预览版已发布](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.6.0)**，当前 `latest` 提供 Linux / Windows 中文安装与 `qqon` 维护。v0.4／v0.5／v0.6 均为预览版，尚无真实业务软件安装和网站部署；标签、产物和既有访问方式保持不变。

## v0.7 开发预览：独立最高权限授权

当前源码新增真实 Linux 软件安装与建站，但 **v0.7.0 尚未发布**；上述一键入口仍下载 v0.6.0，不会提前获得新功能。开发接口、测试和平台边界见 [托管指南](../docs/guide/hosting.md)。

- 新安装可在专门告知后输入 `同意最高权限`；无人值守需单独的 `--accept-system-management`。普通 `--yes` 和升级均不授权。
- Linux 完整管理模式为 root，受限模式保留原 `server-panel` 身份。已有安装先迁移已核实的官方管理器，再升级程序到 ≥v0.7.0（仍保持受限），最后通过 `sudo qqon privilege` 单独备份和迁移。
- 授权模式写入安装记录 format3，固定授权文件为 `/etc/server-panel/management-consent.json`，root 所有、0644。程序独立检查实际身份及记录，网页没有授权或任意 shell 接口。
- Windows 采用独立 LocalSystem 同意／迁移；Linux 业务软件和 Nginx 网站操作不伪装成 Windows 能力。
- 完整管理模式保留 Go 内存软限制、请求／任务／日志上限和较低 CPU/IO 权重；不再把包管理子进程放在旧 128 MiB cgroup 硬上限里。面板、包管理器和业务服务的内存分别衡量。
- 新程序自身不安装 Nginx/PHP/数据库。软件页按需安装后查看真实运行状态，未运行时另行点击启动；不承诺修改开机启用策略。

以下为已发布 v0.6.0 预览版的安装和维护行为。

<a id="v06-开发中中文公网与快捷维护"></a>

## v0.6.0：中文公网与快捷维护

中文四模式向导默认 `public-selfsigned`（确认公网 IPv4、HTTPS `8443`），也可用仅本机、现有 HTTPS 代理或导入证书模式。

安装完成集中输出实际配置 WebUI URL、监听端口、服务状态、证书 SHA-256 指纹／有效期和初始化令牌文件位置。**外网连通性未检测**；请自行确认云安全组、本机防火墙和 NAT。自签证书有浏览器信任提示，须核对指纹；不安装系统信任根，不打印令牌或私钥。

维护入口：

```text
qqon                 中文维护菜单
qqon status          只读状态
qqon url             只读显示配置地址
qqon start           启动固定面板服务
qqon stop            停止固定面板服务
qqon restart         重启固定面板服务
qqon logs            有限日志
qqon update          升级已安装程序
qqon backup          权限受限的一致备份
qqon config          显式改变端口和访问模式
qqon tls renew       自签证书续期
```

Linux 在 `/usr/local/bin/qqon` 安装固定包装入口；Windows 在固定程序目录生成 `qqon.cmd`／`qqon.ps1`，安装器单独确认后才向系统 PATH 追加该目录。当前父终端不会自动刷新，重开终端使用；拒绝追加时给出完整路径。旧 `panel-update` 和 Windows `update.ps1` 仍保留。入口只维护固定服务，不提供任意 root／管理员命令执行。

备份先停服并确认进程退出，取得包含认证、data 和证书的一致副本，敏感备份不公开上传；维护操作按原运行状态恢复服务，原本停止的服务不因备份而被启动。改配置和续期使用新的不可变证书代次，启动失败回滚旧配置；不能通过忽略证书校验让健康探针“通过”。

已有 v0.4/v0.5 先显式迁移已知官方管理器，保留旧访问模式，然后升级应用到 v0.6.0，再通过 `qqon config` 开启公网。未知安装不强制接管，不因新默认值而自动暴露原本仅本机的服务。

## 一行启动交互

Linux，在 root 的 Bash 终端运行：

```sh
curl -fsSL https://linyufiee.github.io/QQON-Panel/install.sh | bash
```

Windows，在管理员 Windows PowerShell 5.1 x64 运行：

```powershell
irm https://linyufiee.github.io/QQON-Panel/install.ps1 | iex
```

入口信任本项目 Pages 和 GitHub Release。下载后的完整安装器先落在受保护的临时目录，核对固定仓库、版本、校验清单，再本地执行；输入从终端读取而不是下载管道。愿意先审阅时可把入口或 Release 安装器保存到文件后执行。

## 交互与系统范围

- 中文菜单提供安装、升级和只读状态，安装后通过 `qqon` 统一维护。
- 新装默认公网 IPv4 自签 HTTPS `8443`，必须确认地址；四种模式与实际端口以向导和配置为准，不开放裸 HTTP 公网。旧 v0.5 默认回环 `127.0.0.1:5173` 的行为保留在旧版中。
- Linux：Debian12/13、Ubuntu22.04/24.04、Rocky9/10、AlmaLinux9/10，amd64/arm64，已运行systemd。APT/DNF仅补缺少的必要依赖；不整机升级、不改源、不关闭SELinux。
- Windows：Windows10/11及Server2019/2022/2025 x64、NTFS、原生SCM服务，无需WSL/Node/Python。真实版本支持与测试状态见文档矩阵。
- `status` / `url` 不安装包、不恢复事务、不改变配置。没有已安装环境时明确提示，未知手动安装拒绝接管；公网地址仅为配置结果。

## 固定身份与路径

Linux：`server-panel`账号、`server-panel.service`、`/usr/local/bin/panel`、`/var/lib/server-panel`私有数据，管理记录`/etc/server-panel/install.json`。

Windows：`QQONPanel`服务、`NT SERVICE\QQONPanel`虚拟账号、ProgramFiles下`QQON-Panel\panel.exe`、ProgramData下`QQON-Panel\data`。程序目录与数据目录使用明确ACL，服务身份不能改写安装器。

管理员凭据没有默认值。初始化令牌和恢复码只保存在私有数据里，不贴到工单、日志截图或仓库。

## 升级和旧版迁移

v0.6.0 使用 `qqon update` 升级已安装程序；兼容的原升级入口仍保留：

```sh
sudo panel-update
```

```powershell
& "$env:ProgramFiles\QQON-Panel\update.ps1"
```

先校验归档和内部ELF/PE，再停服、等待进程退出／文件释放、原子替换、版本健康检查；失败回退并保留可恢复事务。不会恢复旧账户状态或重置数据；重启需要重新登录。

v0.4.0的六资产协议不能读取新九资产协议。已有 v0.4/v0.5 必须显式迁移已知官方管理器，保留原unit、账号和访问配置；迁移成功结束本次操作，程序升级另行执行，新程序健康后才可配置公网。`--yes`不会隐式批准换管理器。修改过的管理器、未知安装及损坏记录不会被强制接管。

发行格式3为三种程序包、四个安装/更新脚本、版本清单和SHA256SUMS共九资产；数据格式仍为1，安装管理记录为format2。归档仅含`panel`或`panel.exe`，拒绝链接、多文件、特殊类型、危险路径、截断、CRC错误与超限展开。

## 维护边界

[v0.6.0 发布 CI #42349315659](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42349315659) 的 `test-build`、`windows-test`、`publish` 均成功，Windows 新增 maintenance 隔离测试也已通过。Linux/Windows CI 不等于真实安装验收；具体系统版本安装、宿主真实 SCM、APT/DNF、SELinux 和物理断电恢复仍未验证。

SHA-256不是独立发布签名；磁盘故障／系统损坏下不承诺绝对恢复。Windows Docker本轮不开放，Linux服务页不是通用Windows服务编辑器。

测试：

```sh
bash -n scripts/bootstrap.sh
bash -n scripts/install.sh
python3 -I -m unittest discover -s scripts/tests
```

```powershell
powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File scripts/windows-tests/test_install.ps1
powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File scripts/windows-tests/test_maintenance.ps1 -PanelBinary C:\path\to\verified-test-panel.exe
```

只对测试进程设置执行策略，不改变系统全局策略；测试使用临时目录与模拟服务，不安装真实宿主服务。
