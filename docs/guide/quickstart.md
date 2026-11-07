# 快速开始

当前已发布预览版 **[v0.6.0](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.6.0)** 可通过下方入口安装。选择系统，复制一条命令，再按中文提示操作。

v0.4.0／v0.5.0／v0.6.0 均不代表稳定成熟，且没有真实业务软件安装与网站创建／部署。**v0.7 预览尚未发布**，已有有限 CI／隔离测试，非全平台验收；[托管开发指南](./hosting)中的最高权限和建站命令不适用于当前 v0.6 下载。

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

## 旧版 v0.4 / v0.5 {#现在安装-v0-4-0}

旧版保留回环／代理访问，不自动切到公网。升级 v0.6.0 先 [显式迁移管理器](./upgrade-migration#manager-migration)，再升级程序，最后按需运行 `qqon config`。固定旧版说明见 [v0.4.0](./legacy-v0-4) 与 [v0.5.0](./releases#下一版本-v0-5-待发布)。
