# 旧版 v0.4.0 安装 {#当前-v0-4-0-安装}

::: info 固定旧版说明
当前已发布预览版是 **[v0.6.0](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.6.0)**，新安装请用 [快速开始](./quickstart)。本页保留 **v0.4.0 预览版**的固定版本安装器与程序，避免 `latest` 选到旧管理器不兼容的 format3。旧版不提供 Windows 安装包、RPM 适配、交互菜单或 v0.6 公网自签默认配置；v0.4／v0.5／v0.6 均没有真实业务软件安装与网站创建／部署。
:::

## 支持条件

Debian 12/13 或 Ubuntu 22.04/24.04，amd64/arm64，Bash、root 权限和正在运行的 systemd。系统 APT 源与 GitHub HTTPS 需要可达。逐系统实机验证边界见 [支持矩阵](./support-matrix)。

不要把旧安装路径用于 Windows、Rocky 或 AlmaLinux，也不要把手动复制的程序当成脚本管理的安装。

## 固定版本下载并审阅

从 [v0.4.0 Release](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.4.0) 下载 `install.sh`。以下命令使用新临时目录，不执行下载内容：

```sh
install_dir="$(mktemp -d)"
curl -fL --proto '=https' --proto-redir '=https' --max-time 120 https://github.com/LinyuFiee/QQON-Panel/releases/download/v0.4.0/install.sh -o "$install_dir/install.sh"
```

核对已知的官方 v0.4.0 安装器摘要：

```sh
printf '%s  %s\n' 'bc78f2ec682704bb5beb965806ec56e511827f6d3451949a03d5b40db535eb91' "$install_dir/install.sh" | sha256sum -c -
less "$install_dir/install.sh"
```

只有摘要一致且审阅完成后才执行。该摘要随本文档分发，不是独立发布者签名；仍需信任项目来源。也可在可信电脑下载、核对并经 SFTP 上传，不必在目标机临时执行不明安装工具。

## 安装 v0.4.0

在上面同一 shell 中执行：

```sh
sudo bash "$install_dir/install.sh" --repo LinyuFiee/QQON-Panel --version v0.4.0
```

已是 root 时省略 `sudo`。旧安装器会列出缺失的最小依赖并请求确认；明确无人值守时才附加 `--yes`。先读安装器帮助，不使用新版的状态参数或交互菜单假设。

已有独立 HTTPS 反向代理时，可在同一安装命令附加 `--origin https://panel.example.com`，将示例替换为真实域名。旧安装器不配置 DNS、TLS、代理或防火墙。

默认监听 **`127.0.0.1:5173`**，不直接开放公网 HTTP；使用 [本机与 SSH 隧道](./installation#local-access)。v0.4.0 不承诺交互选择自定义端口。

## 确认服务与初始化

```sh
sudo systemctl status server-panel.service --no-pager
sudo journalctl -u server-panel.service -n 80 --no-pager
```

以日志提示的本机私有初始化令牌创建管理员；没有默认密码。私有数据目录默认是 `/var/lib/server-panel`，不要分享令牌、账号文件或日志里的私人内容。

程序和管理器由 root 持有，面板以 `server-panel` 低权限账号运行。固定程序、数据、账号与 unit 路径见 [安装说明](./installation#linux-installation)；新版的 `qqon` 入口不属于原 v0.4 安装。

## 旧版本升级边界

v0.4.0 的管理器理解 **format2、六个附件**；v0.5/v0.6 的 format3、九个附件不是它能自动消费的协议。新的 Release 不会默默更换你本地审阅过的安装器。

- 当前已发布预览版 v0.6.0 可下载；v0.7 尚未发布，不要编造 `v0.4.1` 或 v0.7 等未确认存在的下载目标。
- 需要保留 v0.4.0 时继续固定该版本，无需为了“更新”重复安装。
- 升级 v0.6.0 时按 [管理器迁移说明](./upgrade-migration#manager-migration) 显式迁移并保留旧访问，再单独升级程序，最后按需配置公网；不指望旧 `panel-update` 自动学会新格式。
- 旧开发版 format1、手动部署或被修改的管理器不属于自动迁移范围。
