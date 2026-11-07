# 快速开始

当前稳定版 **[v0.5.0](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.5.0)** 已发布。选择系统，复制一条命令，再按终端提示操作。

## 一键交互安装 {#next-release}

**Linux：在 root 的交互式 Bash 中执行（需已有 `curl`）**

```sh
curl -fsSL https://linyufiee.github.io/QQON-Panel/install.sh | bash
```

**Windows：在管理员 Windows PowerShell 5.1（x64）中执行**

```powershell
irm https://linyufiee.github.io/QQON-Panel/install.ps1 | iex
```

随后选择安装、升级或只读状态；首次安装可选择端口（默认 `5173`）和本地／已有 HTTPS 代理访问方式。安装程序不需要 Go、Node、npm、数据库或 Docker。

命令会执行本项目的远程引导脚本。[先下载审阅、wget 入口与安装要求](./installation) · [支持矩阵](./support-matrix) · [发布与 CI 记录](./releases)

下一版 **v0.6 开发中，尚未发布**：[公网 IP HTTPS](./public-https) · [qqon 中文维护](./maintenance)；当前 v0.5 安装仍使用回环监听。

## 访问面板

在自己的电脑建立隧道，把示例主机替换为实际服务器：

```sh
ssh -L 5173:127.0.0.1:5173 your-user@your-server
```

随后打开 `http://127.0.0.1:5173/`。若安装时选了其他端口，请同步替换远端端口，检查本地端口是否占用。Windows 本机安装可直接访问其回环地址；远程 Windows 的 SSH 隧道需要管理员自行配置 SSH 服务，本项目不会代装。

配置了 HTTPS Origin 时，通过对应的 HTTPS 域名访问，不要用不匹配的 Host 或放开 Origin 检查来解决访问问题。安装器不会配置 DNS、证书、代理或防火墙。

## 首次初始化

- 没有默认用户名或默认密码；使用本机私有初始化令牌创建管理员。
- Linux 服务日志可用 `sudo journalctl -u server-panel.service -n 80 --no-pager` 查看。令牌位置以启动输出为准，不把令牌贴到工单或截图中。
- Windows 按安装器和本机私有目录中的实际提示初始化，不能依赖 Linux 日志命令；目录与服务检查见 [Windows 服务](./windows-services)。
- 配置 TOTP 后离线保存恢复码；密码或 TOTP 变更会撤销会话。

接下来阅读 [安全边界](./security) 与 [升级和迁移](./upgrade-migration)。

## 旧版 v0.4.0 {#现在安装-v0-4-0}

需要固定旧版时使用 [v0.4.0 安装说明](./legacy-v0-4)；已有旧版升级到 v0.5.0 时，先 [显式迁移管理器](./upgrade-migration#manager-migration)，再升级程序。
