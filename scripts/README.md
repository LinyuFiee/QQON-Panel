# 一键安装与维护

完整中文文档：**https://linyufiee.github.io/QQON-Panel/**

**[v0.7.0 预览版已发布](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.7.0)**，当前 `latest` 为 v0.7.0。提供中文安装、`qqon` 维护、独立系统管理授权与 Linux 软件／网站流程。旧 v0.4／v0.5／v0.6 均为预览版，没有真实业务软件安装与建站；旧标签、资产及历史行为不改写。

## 一行交互安装

Linux：root 的交互式 Bash，需已有 curl。

```sh
curl -fsSL https://linyufiee.github.io/QQON-Panel/install.sh | bash
```

Windows：管理员 Windows PowerShell 5.1 x64。

```powershell
irm https://linyufiee.github.io/QQON-Panel/install.ps1 | iex
```

入口信任本项目 Pages 和 GitHub Release，将完整安装器下载到受保护的临时目录、校验后执行；交互从终端读取，不从下载管道读取答案。先下载审阅方式见 [安装说明](../docs/guide/installation.md)。不关闭 TLS、ACL 或摘要检查。

## 独立最高权限授权

- 新装专用提示后输入 `同意最高权限`，或独立参数 Linux `--accept-system-management`／Windows `-AcceptSystemManagement`。普通 `--yes`／`-Yes`、update、root/管理员身份本身都不授权。
- 未授权保留 Linux `server-panel`／Windows `NT SERVICE\QQONPanel`；独立同意后面板进程本身为 root／LocalSystem，失陷可影响整机。Windows 的 Linux 软件／Nginx 建站仍是 `not_supported`。
- 已有安装先校验并审阅 v0.7.0 管理器，显式迁移 → `qqon update` 升级程序、受限运行并确认健康 → 如需最高权限再独立 `qqon privilege`。
- Linux 授权命令为 `sudo qqon privilege` 或 `sudo qqon privilege --accept-system-management --yes`；Windows 包装器为 `qqon privilege` 或 `qqon privilege --accept-system-management --yes`。普通升级／状态／管理器迁移不能夹带授权。
- 安装管理记录 format3；Linux 同意记录固定 `/etc/server-panel/management-consent.json`，Windows 固定 ProgramData 下 `QQON-Panel\management-consent.json`。程序独立校验记录与实际身份，网页无授权记录写入或通用 shell 接口。
- 完整模式保留 Go 内存软限制、请求／任务／日志上限及较低 CPU/IO 权重，不把包管理子进程放在旧 128 MiB cgroup 硬限制中；面板、包管理器和业务内存分别衡量。

新装明确授权的例子，仅用于已校验、审阅的完整 v0.7.0 Release 安装器：

```sh
sudo bash install.sh --version v0.7.0 --mode local --yes --accept-system-management
```

```powershell
& .\install.ps1 -Action Install -Version v0.7.0 -AccessMode local -Port 8443 -AcceptSystemManagement -Yes
```

完整路径、Windows 迁移及风险说明见 [托管指南](../docs/guide/hosting.md) 和 [Windows 服务](../docs/guide/windows-services.md#privilege-commands)。

## 访问模式与维护

沿用 v0.6 引入的四模式：默认 `public-selfsigned`（确认公网 IPv4、HTTPS `8443`），或仅本机、现有 HTTPS 代理、导入证书。完成后输出实际配置 URL、端口、服务状态、证书指纹／有效期与初始化令牌位置。**外网连通性未检测**；安全组、防火墙和 NAT 自行确认。自签警告须核对指纹，不安装系统信任根，不公开令牌或私钥。

```text
qqon                 中文维护菜单
qqon status          只读状态
qqon url             只读配置地址
qqon start           启动固定面板服务
qqon stop            停止固定面板服务
qqon restart         重启固定面板服务
qqon logs            有限日志
qqon update          校验并升级程序，不授权最高权限
qqon privilege       独立同意与权限迁移
qqon backup          停服取得私有一致备份
qqon config          显式改变访问配置
qqon tls renew       面板自签证书续期，非网站证书续期
```

Linux 入口 `/usr/local/bin/qqon`；Windows 在固定程序目录生成 `qqon.cmd`／`qqon.ps1`，单独同意追加 PATH 后重开终端，未追加时使用完整路径。原 `panel-update`／Windows `update.ps1` 保留。入口只维护固定面板，不提供任意管理员命令执行。

`status/url` 不安装包、不恢复事务、不改配置。备份先停服并确认退出，含敏感认证／data／证书，保持私有；原停止服务不因备份而启动。配置／换证事务失败保留旧状态，不忽略 TLS 健康校验。

## 软件与建站流程

安装面板不强装 Nginx/PHP/数据库。“软件”页按需提交安装，等待任务完成再查看真实状态；未运行则单独 `start` Nginx／PHP-FPM，确认活动 runtime 后再建站。`install` 不额外 enable/start；APT postinst 常启动，DNF 未必，不承诺开机启用。应用源模板不等于系统源已应用。

站点仅管理自己的 Nginx 配置与 webroot，先 `nginx -t`、reload，再本机标记校验。上传限 2 MiB，UTF-8 编辑限 256 KiB；删除站点保留文件。网站 TLS 只导入已有证书、校验全部域名，非 ACME。

## 系统、身份与路径

- Linux：Debian12/13、Ubuntu22.04/24.04、Rocky9/10、AlmaLinux9/10，amd64/arm64，活动 systemd；安装只补最小依赖，不整机升级、不改源、不关闭 SELinux。
- Windows：Windows10/11 和 Server2019/2022/2025 x64、NTFS、原生 SCM，不需 WSL/Node/Python；不支持 Linux 业务托管。
- Linux：`server-panel.service`、`/usr/local/bin/panel`、`/var/lib/server-panel` 私有数据、`/etc/server-panel/install.json`。服务身份按独立授权为受限账号或 root。
- Windows：`QQONPanel`、ProgramFiles 下 `QQON-Panel\panel.exe`、ProgramData 下 `QQON-Panel\data`，受保护 ACL；受限服务身份不能改写安装器，LocalSystem 仅独立授权后使用。

无默认管理员密码，初始化令牌、恢复码和私钥不进工单、截图或仓库。

## 升级与旧版迁移

Linux 仅识别官方 v0.4/v0.5/v0.6，Windows 仅 v0.5/v0.6；**无 Windows v0.4 支持**。先下载、校验并审阅 v0.7.0 管理器，再显式迁移；未知／改写脚本拒绝接管，未完成事务先恢复。

Windows 从已校验 v0.7.0 脚本执行 `-Action Migrate -Version v0.7.0 -MigrateLegacy -Yes`；`-MigrateLegacy` 只同意旧管理器迁移，不同意 LocalSystem。之后 `qqon update`，受限模式确认健康后才单独 privilege。Linux 同样按迁移 → update → privilege 顺序；普通 yes 不代替任何独立同意。

保留原访问和账号；公网配置另走 `qqon config`。更新先校验归档／内部 ELF/PE，再停服、等待退出／释放文件、原子切换并检查版本；失败回退不等于恢复历史账号数据。完整命令见 [升级与迁移](../docs/guide/upgrade-migration.md)。

v0.7.0 发行清单为 format3 九资产，数据 schema 1；安装管理记录另为 format3。旧 v0.6 安装记录 format2、旧 v0.4 六资产协议仅保留历史，不机械改成新格式。

## 验证边界

[v0.7.0 发布 CI42383705774](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42383705774) 对应 `5d668b229ae57055cb7667e996e50e58c116abba`，test-build/windows-test/publish 成功。主程序阶段 CI42380757509／`fa1483f9…` 的 Linux unit/race/真实托管和 Windows Go＋57PS 是另一提交的阶段记录，不混计。

Windows 57PS 使用 TEMP/mock ACL/SCM，不是真实 LocalSystem 安装；MariaDB/Redis 仅 fake、未真实安装，DNF 未真实测试，网站 HTTPS 仅单元/loopback，完整 GUI 因后续截图工具超时未完成。SHA-256 不是独立签名，磁盘损坏不保证恢复；详见 [支持矩阵](../docs/guide/support-matrix.md)。

本地脚本测试使用仓库已有隔离测试，不执行真实宿主服务安装；只对测试进程设置执行策略，不改全局策略。
