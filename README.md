# QQON-Panel

**一个面向个人开发者的轻量服务器面板：少一点常驻服务，把资源留给你的业务。**

QQON-Panel 使用 Go 标准库后端和原生 HTML / CSS / JavaScript 前端。网页内嵌在单个程序中，不需要常驻 Node、Redis、数据库或容器引擎；界面清爽，功能按需启用。

[最新版本](https://github.com/LinyuFiee/QQON-Panel/releases/latest) · [安装与升级](scripts/README.md) · [发布指南](RELEASING.md)

> **早期开发版本。** 已实现真实账号、安全设置、资源读取和受控 Docker 接口，但不是已完成生产安全审计的成熟面板。默认仅监听本机，不自动安装数据库、Docker 或网站环境。

## 功能

| 模块 | 当前能力 |
| --- | --- |
| 概览 | Linux CPU、内存、磁盘、网络、运行时间；固定容量趋势；面板自身内存信息 |
| 个人与安全 | 管理员初始化、登录退出、修改用户名／密码、TOTP、一次性恢复码、会话撤销、安全审计 |
| 文件 | 指定根目录内分页浏览、文本只读预览；路径与文件大小限制 |
| 服务 | 固定白名单的 systemd 服务状态读取 |
| 应用与源 | 清华、阿里、自定义 APT 与 Docker CE 源，以及独立的 Registry 配置；目前只保存和预览 |
| Docker | 显式连接本机 Unix socket，读取容器／镜像／卷／网络和限量日志；可选择启用确认后的启动／停止／重启 |
| 面板更新 | 手动检查本仓库 GitHub Release，展示版本与升级指引；管理员通过脚本升级 |

网站部署、Compose、应用商店、文件编辑、终端、数据库管理和自动证书申请尚未实现。不可用的系统指标明确显示“不可用”，不使用模拟数据冒充真实状态。

## 支持环境

- **Debian 12 / 13，Ubuntu 22.04 / 24.04**。
- **amd64（x86_64）、arm64（aarch64）**。
- Bash、root 安装权限，以及已运行的 systemd。
- GitHub HTTPS 可达，系统 APT 源正常。

安装器会检查并补齐必要依赖：Python 3、CA 证书及确实缺失的账号／系统工具。只安装缺失的最小包，不运行整机升级、不修改软件源，不安装 Go、Node、Git、数据库或 Docker。非 systemd 容器不受支持，安装器不会强装 systemd。

Windows 支持本地开发，但 Linux 主机指标、systemd 和 Docker Unix socket 能力不在 Windows 上运行。

## 快速安装

### 1. 下载安装器

从 [最新 Release](https://github.com/LinyuFiee/QQON-Panel/releases/latest) 下载 `install.sh` 到目标服务器并先审阅。若服务器已有 curl，可用：

```sh
curl -fL --proto '=https' \
  https://github.com/LinyuFiee/QQON-Panel/releases/latest/download/install.sh \
  -o install.sh
```

也可在浏览器下载后通过 SFTP 上传到服务器，不必为了安装面板先安装 curl。不要直接执行未知来源的 `curl | sh`。

### 2. 执行

```sh
sudo bash install.sh --repo LinyuFiee/QQON-Panel
```

已是 root 时省略 `sudo`。脚本列出缺失依赖并请求确认，随后自动完成补齐、下载对应架构的 `.tar.gz`、校验、受限解压和 systemd 安装。明确无人值守执行时使用 `--yes`：

```sh
sudo bash install.sh --repo LinyuFiee/QQON-Panel --yes
```

需要预设 HTTPS 反向代理来源时：

```sh
sudo bash install.sh --repo LinyuFiee/QQON-Panel \
  --origin https://panel.example.com
```

安装器不会替你配置域名、证书、反向代理或防火墙。

### 3. 打开并初始化

默认监听服务器的 **127.0.0.1:5173**，不直接暴露公网。可从自己的电脑建立 SSH 隧道：

```sh
ssh -L 5173:127.0.0.1:5173 your-user@your-server
```

在电脑浏览器打开 `http://127.0.0.1:5173/`。首次需读取服务器上的私有初始化令牌并设置自己的管理员用户名、强密码；**没有默认密码**。令牌路径由服务日志提供，脚本默认数据目录为 `/var/lib/server-panel`。

如果安装时配置了 HTTPS Origin，请通过该独立域名的 HTTPS 代理访问，而不是直接通过不匹配的 Host 访问。不要在面板相同来源托管用户网站或上传文件。

## 后续升级

面板内打开“面板更新”，点击“检查更新”。只在手动操作时请求 GitHub，页面展示会使用缓存，无常驻更新任务。

在已经通过脚本安装的服务器执行：

```sh
sudo panel-update
# 或指定某个已发布的新版本
sudo panel-update --version v0.4.1
```

升级先验证压缩包和解压后程序的大小、SHA-256、架构、版本与数据格式，再保留旧程序、停服、原子切换，最后核对实际运行版本。失败尝试回退；未完成的升级保留事务记录，下次执行先恢复，不自动反复尝试失败版本。

- 保留管理员、TOTP、源配置等私有数据。
- 重启后内存会话失效，需要重新登录。
- 同版本不重复安装，普通更新拒绝降级和不兼容的数据格式。
- 网页进程不获得 root；安装与升级由管理员显式运行。
- SHA-256 是完整性校验，不是独立发布签名；此版本信任本仓库和 HTTPS。

完整路径、依赖、旧安装器迁移和恢复边界见 [安装说明](scripts/README.md)。

## 轻量化设计

- Go 单进程、标准库；无外部 Go 模块或前端框架运行时。
- 一份周期采样供所有客户端读取，趋势固定 300 点，systemd 查询缓存。
- 一般请求、连接、登录密码派生、会话、任务队列及输出均有限额。
- Docker 单 worker，等待队列 8，保留任务 32；日志最多 128 KiB，文件预览最多 256 KiB。
- 前端切页取消旧请求，页面隐藏时暂停监控轮询。
- 业务进程与面板解耦，面板不默认启用重型依赖。

目标预算为 1 vCPU / 512 MiB 服务器上的低常驻开销。**这不是已通过所有目标机器的性能承诺**；Go 堆、RSS 和整个主机用量不是同一指标。请在自己的业务负载下测量，不能将本地短测当作生产稳定性验收。

## 安全默认值

- 低权限 `server-panel` 账号运行，程序／安装器／unit 由 root 持有。
- 初始化令牌、PBKDF2 密码派生、TOTP、CSRF、HttpOnly／SameSite Strict Cookie、登录限流、会话撤销。
- 默认回环监听；直接公网监听必须提供 TLS 和明确 HTTPS Origin。
- 固定静态资源白名单，不提供源代码、项目目录或私有配置下载。
- 文件浏览必须显式授权根目录，且不能包含面板私有数据。
- Docker socket 必须显式配置，写操作默认关闭。即使网页接口只读，持有 Docker socket 的进程也可能具备很高主机权限，优先评估 rootless Docker。
- 不信任任意代理头，不接受网页传入的 shell 命令、任意更新 URL 或任意 unit 名。

密码／TOTP 变更会撤销会话；恢复码只显示一次。停止面板后备份私有数据目录，备份同样需要严格权限或加密。不要把账户 JSON、初始化令牌或日志中的私人数据提交到 GitHub。

## 开发与构建

需要 Go **1.25+**；建议使用当前受支持的稳定工具链。无需 npm。

```sh
go test ./...
go vet ./...
go build -trimpath -ldflags="-s -w" -o panel .
./panel -listen 127.0.0.1:5173 -data-dir /private/path/panel-data \
  -github-repo LinyuFiee/QQON-Panel
```

Windows 编译为 `panel.exe`，使用本机私有目录作为 `-data-dir`。不要把生产账号用于界面测试。`-build-info` 可输出公开版本身份而不启动服务。

```sh
bash -n scripts/install.sh
bash -n scripts/update.sh
python3 -I -m unittest discover -s scripts/tests
python3 -I -m unittest discover -s tools/tests
go test -race ./...  # 需要受支持的平台和 C 编译工具链
node --check app.js
node --check api.js
```

发行构建包含 `panel-linux-amd64.tar.gz`、`panel-linux-arm64.tar.gz`、`install.sh`、`update.sh`、`release.json`、`SHA256SUMS`。压缩包内只有 `panel` 普通文件，解压拒绝路径穿越、链接、特殊文件、多成员和超限内容。

维护者推送 `vMAJOR.MINOR.PATCH` 标签后，由 Actions 测试、打包并创建 Release。详见 [发布指南](RELEASING.md)。

## 当前限制

- 系统应用源配置与安装器补依赖是两回事：应用页不会自动安装 Nginx、MySQL、PHP 或 Docker。
- 实际目标机 Linux/systemd、Docker、文件权限和升级恢复需要在测试机验收，跨编译或模拟测试不能替代。
- 不保证断电、文件系统损坏或持续磁盘写入失败时总能自动恢复。
- 暂无多用户权限、邮件找回、企业审计或生产安全认证。
- 当前未指定开源许可证；仓库公开可见不代表授予任意再分发许可。
