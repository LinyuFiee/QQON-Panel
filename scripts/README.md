# 一键安装与维护

完整中文文档：**https://linyufiee.github.io/QQON-Panel/**

> **[v0.5.0 已发布](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.5.0)**，提供 Linux / Windows 交互安装入口。v0.4.0 保留为旧版，不覆盖其公开产物。

## v0.6 开发中：中文公网与快捷维护

当前网络入口仍按已发布 latest 工作；v0.6 正式发布前不会把旧安装器冒充新功能。新源码提供中文四模式向导：默认 `public-selfsigned`（确认公网 IPv4、HTTPS 8443），也可用仅本机、现有 HTTPS 代理或导入证书模式。

安装完成集中输出实际配置 WebUI URL、监听端口、服务状态、证书 SHA-256 指纹／有效期和初始化令牌文件位置。**这不等于外网连通检查通过**；请自行确认云安全组、本机防火墙和 NAT。自签证书有浏览器信任提示，不安装系统信任根，不打印令牌或私钥。

v0.6 维护入口：

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

Linux 在 `/usr/local/bin/qqon` 安装固定包装入口；Windows 在固定程序目录生成 `qqon.cmd`／`qqon.ps1`，安装器单独确认后才向系统 PATH 追加该目录。当前父终端不会自动刷新，重开终端使用；拒绝追加时给出完整路径。旧 `panel-update` 和 Windows `update.ps1` 仍保留。

备份包含认证和证书敏感信息，不公开上传；维护操作按原运行状态恢复服务，原本停止的服务不因备份而被启动。改配置和续期使用新的不可变证书代次，启动失败回滚旧配置；不能通过忽略证书校验让健康探针“通过”。

已有 v0.4/v0.5 先显式迁移已知官方管理器，保留旧访问模式，然后升级应用到 v0.6，再通过 `qqon config` 开启公网。未知安装不强制接管，不因新默认值而自动暴露原本仅本机的服务。

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

- 菜单：安装、升级、只读状态、退出。
- 首次可选端口（1024–65535，默认5173）与本机／SSH隧道或已有HTTPS代理；不默认开放裸HTTP公网。
- Linux：Debian12/13、Ubuntu22.04/24.04、Rocky9/10、AlmaLinux9/10，amd64/arm64，已运行systemd。APT/DNF仅补缺少的必要依赖；不整机升级、不改源、不关闭SELinux。
- Windows：Windows10/11及Server2019/2022/2025 x64、NTFS、原生SCM服务，无需WSL/Node/Python。真实版本支持与测试状态见文档矩阵。
- 状态操作不安装包、不恢复事务、不改变配置。没有已安装环境时明确提示，未知手动安装拒绝接管。

## 固定身份与路径

Linux：`server-panel`账号、`server-panel.service`、`/usr/local/bin/panel`、`/var/lib/server-panel`私有数据，管理记录`/etc/server-panel/install.json`。

Windows：`QQONPanel`服务、`NT SERVICE\QQONPanel`虚拟账号、ProgramFiles下`QQON-Panel\panel.exe`、ProgramData下`QQON-Panel\data`。程序目录与数据目录使用明确ACL，服务身份不能改写安装器。

管理员凭据没有默认值。初始化令牌和恢复码只保存在私有数据里，不贴到工单、日志截图或仓库。

## 升级和旧版迁移

Linux安装后的普通升级：

```sh
sudo panel-update
```

Windows安装后的普通升级：

```powershell
& "$env:ProgramFiles\QQON-Panel\update.ps1"
```

先校验归档和内部ELF/PE，再停服、等待进程退出／文件释放、原子替换、版本健康检查；失败回退并保留可恢复事务。不会恢复旧账户状态或重置数据；重启需要重新登录。

v0.4.0的六资产协议不能读取新九资产协议。新Linux安装器只允许官方已知旧管理器，通过单独终端确认备份与迁移管理器，保留原unit、账号和配置。迁移成功会结束本次操作，下一次`panel-update`再升级应用。`--yes`不会隐式批准换管理器。修改过的管理器、未知安装及损坏记录不会被强制接管。

格式3为三种程序包、四个安装/更新脚本、版本清单和SHA256SUMS共九资产；数据格式仍为1。归档仅含`panel`或`panel.exe`，拒绝链接、多文件、特殊类型、危险路径、截断、CRC错误与超限展开。

## 维护边界

[v0.5.0 发布 CI #42328127375](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42328127375) 的 `test-build`、`windows-test`、`publish` 均成功。Linux/Windows CI 与隔离测试不等于真实安装验收；具体系统版本安装、真实 SCM、APT/DNF、SELinux 和物理断电恢复仍未验证。

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
