# 快速开始

当前已发布预览版 **[v0.7.0](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.7.0)** 可通过下方入口安装。选择系统，复制一条命令，再按中文提示操作。

v0.7.0 提供独立授权后的 Linux 软件安装和建站，仍不是全平台验收承诺；Windows 业务软件／网站为 `not_supported`。旧 v0.4.0／v0.5.0／v0.6.0 均为预览版，没有真实软件安装和网站部署，需先按 [迁移顺序](./upgrade-migration#manager-migration) 升级。

## 一键交互安装 {#next-release}

**Linux：在 root 的交互式 Bash 中执行（需已有 `curl`）**

```sh
curl -fsSL https://linyufiee.github.io/QQON-Panel/install.sh | bash
```

**Windows：在管理员 Windows PowerShell 5.1（x64）中执行**

```powershell
irm https://linyufiee.github.io/QQON-Panel/install.ps1 | iex
```

确认自己的公网 IPv4，新安装默认自签 HTTPS、端口 `8443`；也可选择仅本机、已有 HTTPS 代理或导入证书。命令会执行本项目的远程引导脚本，[审阅方式、wget 入口与四种模式](./installation)见安装说明。

## 访问面板

打开安装结果中的地址，默认形式为 `https://<已确认的公网IPv4>:8443/`。自签证书会有浏览器警告，请与可信终端显示的 SHA-256 证书指纹核对；不一致时停止访问。

链接仅表示配置结果，**未检测外网可达性**；云安全组、防火墙和 NAT 需人工配置。详见 [公网 HTTPS](./public-https)。仅本机模式或旧版回环访问见 [本机与 SSH 隧道](./installation#local-access)。

## 首次初始化

按安装结果定位本机私有初始化令牌，设置自己的管理员账号与强密码；没有默认密码，不分享令牌、私钥或恢复码。安装后运行 `qqon` 进入 [中文维护菜单](./maintenance)。

[支持与验证边界](./support-matrix) · [安全边界](./security) · [发布与 CI 记录](./releases)

## Linux 开始建站

1. 新装时独立同意最高系统权限，或在匹配的 v0.7.0 安装上运行 `sudo qqon privilege` 并输入 `同意最高权限`；普通 yes/update 不授权。
2. 在“软件”页按需安装 Nginx，PHP 网站另装 PHP-FPM，等待任务完成并看实际服务状态。
3. 对未运行的服务显式“启动”，确认 Nginx／PHP runtime 可用后再在“网站”页建站。安装不额外 enable/start，不承诺开机自启。

详细限制见 [托管指南](./hosting)。Windows 即使授权 LocalSystem，也不提供 Linux 软件／Nginx 建站。

## 旧版 v0.4 / v0.5 / v0.6 {#现在安装-v0-4-0}

先下载、校验并审阅 v0.7.0 管理器，[显式迁移](./upgrade-migration#manager-migration) → `qqon update` 升级程序、保持受限并确认健康 → 如需最高权限再独立 `qqon privilege`。不重置账号、不自动开放旧访问。Linux 支持已知 v0.4/v0.5/v0.6，Windows 仅 v0.5/v0.6，无 Windows v0.4；固定旧版说明见 [v0.4.0](./legacy-v0-4) 与 [历史发行](./releases#当前已发布-v0-6-0)。
