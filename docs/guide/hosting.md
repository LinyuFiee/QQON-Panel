# v0.7.0 预览版托管指南

::: warning 预览能力与验证边界
本页适用于 **[v0.7.0 预览版](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.7.0)** 的最高系统权限、Linux 软件安装、真实网站和任务流程。程序已发布，但有限 CI／隔离测试不等于稳定成熟或全平台验收，详见 [验证记录](#verification)。新装用 [安装说明](./installation)，旧安装先迁移已校验的 v0.7.0 管理器再升级程序；不要把新参数直接交给旧安装器。

**v0.4.0／v0.5.0／v0.6.0 没有真实业务软件安装和网站创建／部署能力。** 这些已发布版本的“应用与源”配置／模板不能当作软件已安装或网站已上线。
:::

## 平台与授权前提 {#authorization}

| 模式／平台 | 服务身份与能力 |
| --- | --- |
| 未授权的受限模式 | Linux `server-panel`／Windows `NT SERVICE\QQONPanel`；保留受限监控等既有能力，软件与网站管理须明确显示不可用原因 |
| 已独立授权的 Linux | 面板进程本身以 **root** 运行；还需可信 APT/DNF、活动 systemd、可发现的受支持软件与安全路径 |
| 已独立授权的 Windows | 面板进程本身以 **LocalSystem** 运行；**不支持 Linux APT/DNF 软件安装或 Nginx 静态／代理／PHP 建站** |

最高权限不是临时助手，也不是只批准某一次安装；面板漏洞或管理员账户失陷可能影响整台主机。不要把它视为不可信租户隔离平台。登录、CSRF、Host/Origin 校验、限流和操作确认继续保留，但不能把 root 进程变回操作系统沙箱。

### 独立同意与命令边界

以下接口需匹配的 v0.7.0 程序与管理器。Linux／Windows 命令和平台能力不同：Ubuntu 真实托管 CI 仅覆盖列出的场景；Windows 本机及阶段 CI 的 57 项仍是 TEMP/mock ACL/SCM，不是真实 LocalSystem／SCM 安装验收。

| 场景 | 专用接口 | 不能替代的同意 |
| --- | --- | --- |
| Linux 新安装 | 已审阅本地安装器的 `--accept-system-management` | root 身份、`--yes`、普通安装确认均不等价 |
| Windows 新安装 | 已审阅本地安装器的 `-AcceptSystemManagement` | 管理员 PowerShell、`-Yes`、追加 PATH 同意均不等价 |
| 已有受管理安装 | 新维护入口 `qqon privilege`；Windows 管理器对应 `-Maintenance privilege` | `qqon update`、普通升级、访问配置或换证均不授权 |
| 交互同意 | 专用最高权限提示后输入 **`同意最高权限`** | 普通 `y`／`yes` 不等价；拒绝时保持受限模式 |

旧安装不得因升级自动提权。Linux 已知官方 v0.4/v0.5/v0.6 的顺序是：**先校验并显式迁移 v0.7.0 管理器 → `sudo qqon update` 到 v0.7.0、保持 restricted 并确认健康 → 如需 root 再 `sudo qqon privilege` 单独授权**。保留原访问和账号；先备份，未知或改写管理器不接管。

#### Linux v0.7.0 命令

已有受管理安装完成上述管理器迁移与程序升级后，可二选一：

```sh
# 交互授权：阅读最高权限告知，再准确输入“同意最高权限”
sudo qqon privilege

# 已阅读并明确同意后，使用专用标志表达授权
sudo qqon privilege --accept-system-management --yes
```

语法为 `sudo qqon privilege [--accept-system-management] [--yes]`，方括号表示可选项，不要原样输入。独立的 `--accept-system-management` 才是非交互同意；`sudo qqon privilege --yes` 单独使用不授权。当前已是 root 时可省略 `sudo`。

仅对**新安装**，先从 v0.7.0 Release 获取、校验并审阅 `install.sh`，再选择仅本机模式和独立授权：

```sh
sudo bash install.sh --version v0.7.0 --mode local --yes --accept-system-management
```

这里使用完整的 v0.7.0 Release 安装器，不混用旧版本或不明分支脚本。若实际程序版本低于 v0.7.0，最高权限请求会拒绝；旧版本只能受限运行。

- `--accept-system-management` 只允许新安装或独立的 `qqon privilege`；已有安装不能靠重复安装获得授权。
- 普通 `--upgrade`、`--update`、`--status`、`--migrate-manager` 与授权标志组合均拒绝；`qqon update/status` 也不能附带授权标志。
- Linux bootstrap 识别 v0.7.0 安装器 **protocol 5** 并转发参数；旧 v0.6 安装器与旧产物保持不变，不因新入口发布自动获得最高权限。
- 安装器 protocol 5、安装管理记录 format3、发行清单 format3 和用户数据 schema 1 各有独立含义，不可互换。

#### Windows v0.7.0 命令

在管理员 PowerShell 中，新管理器／程序已就绪后，可运行 `qqon privilege` 并按提示输入 `同意最高权限`，或在已阅读告知后运行 `qqon privilege --accept-system-management --yes`。Windows 包装入口接受双横线参数，直接调用安装器则用 `-Maintenance privilege -AcceptSystemManagement -Yes`，不要混写两种语法。

完整的带引号 PowerShell 路径调用、新装 `-Action Install -Version v0.7.0 -AccessMode local -Port 8443 -AcceptSystemManagement -Yes` 示例见 [Windows 服务命令](./windows-services#privilege-commands)。已知 Windows v0.5/v0.6 管理器先通过已校验的 v0.7 脚本 `-Action Migrate -Version v0.7.0 -MigrateLegacy -Yes` 独立迁移，再 `qqon update`、确认受限程序健康，最后单独 privilege；**无 Windows v0.4 支持**。`-MigrateLegacy` 同意的是管理器迁移，不是 LocalSystem 授权。

这些命令适用于已发布的匹配 v0.7.0 安装；本机和阶段 CI 隔离测试通过不代表真实 LocalSystem／SCM 安装验收。

未完成事务先恢复；未知或被修改的管理器不能强制接管。具体顺序见 [升级与迁移](./upgrade-migration#privilege-migration)。

权限迁移须停服取得一致备份，保护服务身份、数据所有权／ACL、同意记录、安装配置与管理器；失败按事务恢复并保留错误。不要改 unit／SCM 身份、删锁、放宽 ACL 或手写同意 JSON 来绕过流程。

### 同意记录不由网页创建

- Linux：固定 `/etc/server-panel/management-consent.json`，root 持有，记录 `0644`、父目录 root 持有且 `0755`。
- Windows：系统 ProgramData 下 `QQON-Panel\management-consent.json`，与 data 分开，受保护 ACL 仅 Administrators/SYSTEM；不假定系统盘一定是 `C:`。
- 程序同时检查记录、路径／所有权或 ACL 和**实际 root／LocalSystem 身份**。只有 root 身份、只有记录、或旧记录残留均不足以授权。
- 网页只读显示管理状态（`GET /api/management` 的 `authorized`、`mode`、`identity`、`reason` 等），没有网页授权按钮或写入同意记录的 API；`authorized=true` 也不能越过 Windows 平台限制。

v0.7.0 的**安装管理记录**为 format3，新增 `privilegeMode`／`privilegeConsent`；旧 format1/2 默认为 restricted，普通升级锁定权限字段。这不是发行清单 format3 或用户数据 schema 1 的变更。

## Linux 真实软件流程 {#apps}

1. 在“软件”页查看真实能力和状态（`GET /api/apps`）。未授权、非 Linux、缺少可信包管理器／systemd 时显示原因，不显示假安装成功。
2. 从白名单 **Nginx、PHP-FPM、MariaDB、Redis** 选择所需软件，查看对系统的影响并单项确认。
3. 提交安装请求（`POST /api/apps/{id}/install`，需明确操作确认），在“任务”页等待终态。HTTP `202` 和任务 ID 只代表排队，不代表已完成。
4. 回软件页核对包版本、安装状态与**真实服务状态**。`install` 只调用包管理器安装并查询结果，不额外执行 `enable`／`start`；安装任务成功不等于服务正在运行。任务开始时仍须重新检查系统管理授权，不以下载结束、进度条或命令退出码代替后置检查。
5. **Nginx 未运行时，先在“软件”页显式选择“启动”**（`start`，对应 `POST /api/apps/nginx/start`），确认后等待任务完成并重新查看状态。PHP 站点还需启动 PHP-FPM（`POST /api/apps/php-fpm/start`），不能只安装包就直接建站。停止／重启也走单独确认的 `stop`／`restart` 任务。
6. 确认 Nginx 可用且运行后再建站；PHP 站点还需实际发现的活动 PHP-FPM runtime/socket。选择当前返回的 runtime ID，不硬编码“PHP 8.3”或让用户随意填路径。

**发行版行为不同**：APT 包的 postinst 常会启动服务，DNF 安装后未必启动；这是系统包脚本／策略的结果，不是面板 `install` 的自动启动保证。流程始终是“安装 → 看状态 → 对未运行的 Nginx／PHP-FPM 显式 `start` → 确认运行 → 建站”。`start` 也不等于 `enable`，本轮不承诺自动设置业务软件开机自启；需管理员按实际发行版策略另行确认。

软件页是受控操作，不是任意包名／版本／脚本执行入口；没有卸载、数据库重置、任意仓库 URL 或整机升级。已有软件不等于允许接管其配置，MariaDB／Redis 的账户、数据、安全监听与业务调优仍由管理员负责。PHP-FPM 服务动作可能作用于全部已发现的发行版 PHP-FPM 单元，确认前评估对既有站点的影响。

### 可信系统源与模板不是一回事 {#trusted-sources}

- APT 使用主机**已配置的可信发行版仓库**，刷新索引后安装白名单包；DNF 使用现有可信仓库。实际版本由发行版及其源决定，不承诺固定上游最新版。
- “应用与源”页面的 APT／Docker CE／Registry 配置和模板不会自动变成系统源；选模板不表示源已应用，也不会触发真实业务软件安装。
- 不自动加第三方源、不替换管理员源、不关闭包签名或 TLS、不关闭 SELinux、不执行整机升级；仓库缺包、锁占用、网络或策略失败应返回任务错误。
- 安装超时、失败或中断不保证系统包事务已回滚。先核对实际包与服务状态再决定下一步，不连点重试，不把“任务失败”误解为“系统完全未变”。

## Linux 真实网站流程 {#sites}

### 创建与更新

1. 确认软件依赖、授权和 Nginx **实际运行**状态；若提示未运行，先返回“软件”页显式 `start` Nginx 并等待任务结果，不重复安装或跳过依赖检查。PHP 站点同样须先 `start` PHP-FPM 并确认活动 runtime/socket；条件满足后再在“网站”页选择**静态、反向代理或 PHP**。
2. 填写名称、域名／IP、端口及启用状态。域名数量最多 10；端口只允许 `80`、`443` 或 `1024–65535`，不能占用面板端口。站点主机不能与面板主机或其父子域重叠，不靠不同端口隔离 Cookie。
3. 代理站点填写经过校验的 HTTP(S) upstream，无用户凭据、查询或片段；不能指向面板或被限制的本机管理端口。反向代理可以暴露原本仅本机的业务服务，是管理员需主动评估的能力，不自动启动 upstream。
4. PHP 站点从实际发现的 PHP-FPM runtime 中选择，不写任意 socket／命令／Nginx 指令。没有可用 runtime 时先解决依赖，不创建假 PHP 站点。
5. 确认后创建或更新（`POST /api/sites`／`PATCH /api/sites/{id}`），等待任务终态。更新携带当前 `revision`；冲突时刷新重读，不覆盖其他更改。

网站根由面板固定管理为 `/srv/qqon-sites/{id}`，配置为 `/etc/nginx/conf.d/qqon-{id}.conf`，`id` 是内部标识而非域名。不能从网页自选主机路径，也不会接管未知网站目录、主 `nginx.conf`、`sites-enabled` 或别人维护的 server block。没有针对不可信站点的容器／租户隔离保证。

### Nginx 校验与“成功”的含义

- 对现有受管配置核对标记／摘要，拒绝未知文件、链接或外部改写；新配置按事务写入。
- **先 `nginx -t`，通过后再 reload**，再经本机 Host 路由检查自身站点标记 `/.__qqon_health`，防止把其他默认站点的 HTTP 响应当作成功。
- 语法、端口、reload 或站点标记检查失败，任务必须显示失败并按事务恢复旧配置／元数据；恢复本身失败则保留诊断，不假报成功。不能跳过 `nginx -t`、覆写主配置或杀死未知占用服务来“修复”。
- 标记验证只证明面板的有限本机路由检查，不等于全部业务路径、PHP 应用、代理 upstream 或外网可用。新站点内容、代理响应和 PHP 执行仍需单独检查。
- 网站 URL 是配置结果，不验证公网 DNS、云安全组、防火墙、NAT 或外部证书信任；需管理员从受控外部网络另行验收。

### 停用与删除

停用按受管配置返回 `503`，避免落到默认站点继续服务内容；不是卸载 Nginx。删除站点（`DELETE /api/sites/{id}`）移除面板受管站点配置，**保留网站根与文件**，不等于删除业务数据。本轮没有通用递归文件删除、未知站点接管或数据库删除接口。操作后仍检查任务与实际状态。

## 有限网站文件上传与编辑 {#files}

本页新增文件能力仅作用于**受管站点根**；与既有“文件”页的授权根只读浏览不同，不提供整个主机的文件管理器。

| 操作 | 限制与冲突处理 |
| --- | --- |
| 目录列表 | 相对路径、有界扫描，每页最多 200 条；继续读取分页，不一次加载整个目录树 |
| 单文件上传 | 最多 **2 MiB（2,097,152 字节）**，仅普通文件；不解压 ZIP/tar、不批量递归上传 |
| 文本查看／编辑 | UTF-8，最多 **256 KiB（262,144 字节）**；编辑以读取时 SHA-256 检测并发修改 |
| 已有同名上传目标 | 拒绝覆盖，返回冲突；需要文本修改时重读内容并通过编辑流程保存 |
| 路径与文件类型 | 不越出受管根，拒绝不安全链接／硬链接／特殊文件；不接受任意绝对路径 |

上传、编辑仍需登录、同源／CSRF 和显式操作确认，并返回任务结果；不能通过分块或改请求绕过上限。不要上传账号数据、初始化令牌、TOTP、证书私钥或备份到 webroot。文本编辑接口本身不执行内容，但**保存的 PHP 代码可能被 PHP-FPM 执行**，静态 HTML/JS 也会在访客浏览器执行；只部署可信代码，不把路径限制当成代码隔离。

## 网站 TLS 是导入，不是 ACME {#site-tls}

网站 TLS 使用 `POST /api/sites/{id}/tls` 导入管理员已有的 PEM 证书链与私钥；需当前 revision 和明确确认，按任务更新 Nginx。证书须在有效期内、与私钥匹配，**对站点配置的每一个域名／IP 都通过 SAN 主机校验**，不再只验证首个域名；任一域名未覆盖即拒绝。端口与 TLS 配置必须兼容，失败停止并显示原因。

当前网站 HTTPS 导入仅完成单元／loopback 测试，**尚未验证真实 Nginx HTTPS**；Ubuntu 真实站点 HTTP 成功不能作为导入证书后的 HTTPS 验收。

证书最多 **64 KiB**、私钥最多 **16 KiB**，导入载荷另有上限。私钥保存在面板受保护的私有证书目录，不在网站根；不回传、不写任务日志，也不把 PEM 保存在重启后任务描述中。备份仍可能包含证书，需要私密保存。

**没有 ACME 自动申请／续期、DNS 验证或公共 CA 信任保证**。到期与换证由管理员安排；`qqon tls renew` 是面板自身的自签证书维护，不续期网站导入证书。面板访问 TLS 另见 [公网 HTTPS](./public-https)。

## 共享任务与失败处理 {#jobs}

软件和网站共用单个 worker，最多 **8 个等待任务、32 条保留记录**；单任务日志只保留有限尾部（上限 64 KiB），不是完整无限日志服务。通过 `GET /api/jobs`／`GET /api/jobs/{id}` 或“任务”页查看阶段、进度、状态与脱敏诊断，没有任意命令执行接口。

| 状态 | 含义 |
| --- | --- |
| `queued` | 已接受并持久化描述，尚未执行 |
| `running` | 正在执行受控操作 |
| `succeeded` | 该操作及其既定后置检查完成，不等于业务或外网全验收 |
| `failed` | 操作或检查失败；可能已部分修改主机，需查看实际状态与恢复结果 |
| `interrupted` | 面板重启或运行中断；不会自动重放之前的写操作 |

刷新网页不等于重试任务；重复操作或队列满会拒绝请求，先查看已有任务。重启后排队／执行中任务转为 interrupted，网站事务恢复与任务重放是两回事。保留任务 ID、阶段和脱敏错误，核对实际包、服务和受管配置后再决定重试；不要删除记录或反复点击来掩盖失败。

## 面板 RAM 与业务 RAM 分开 {#memory}

- **面板自身**：Go 堆、进程 RSS／Windows 工作集、面板任务与缓存；这些指标之间也不相等。
- **工作负载**：Nginx、PHP-FPM worker、MariaDB、Redis、代理 upstream 与用户程序分别占用内存，不计入面板进程自身指标。
- **主机总量**：包含面板、业务、操作系统及缓存；装更多软件、增大 worker／数据库缓存会减少可用内存。

“轻量面板”不是“整套网站和数据库几乎不占 RAM”。各 5 次操作后短时 process RSS 采样：`80fea3b505bc429541ba31ed2f1ac751debaaf9e`／CI42379665605 中位数＝最大值 **15.42 MiB**；`fa1483f9ec57294d000a6f71014c4e6ee7afe87c`／CI42380757509 中位数 **15.51 MiB**、最大值 **15.52 MiB**。两次记录独立，不含业务 RAM，也不是发行标签新测量、SLA、soak 或全负载保证。实际并发与数据负载仍须分别测量面板／业务／主机。

## 当前验收与非目标 {#verification}

**v0.7.0 已发布，以下验证仍有明确范围。** [发布 CI42383705774](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42383705774) 对应发行提交 `5d668b229ae57055cb7667e996e50e58c116abba`，三个任务成功并公开 Release；[主程序阶段 CI42380757509](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42380757509) 对应 `fa1483f9ec57294d000a6f71014c4e6ee7afe87c`，Linux unit／race／真实托管与 Windows Go＋57PS 成功。两类 CI 不混作同一提交的实机验收，Windows 仍非真实 SCM／LocalSystem 安装；下节保留更早 `80fea3b…` 的独立场景。

### Ubuntu 真实托管 CI

[run #42379665605](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42379665605) 已成功，固定提交为 `80fea3b505bc429541ba31ed2f1ac751debaaf9e`。测试在专用 Ubuntu CI 环境真实执行：

- 缺包安装 Nginx `1.24.0-2ubuntu7.19` 和 PHP `8.3.6-0ubuntu0.24.04.13`，再检查／启动服务；不改变 `install` 不额外 enable/start 的接口边界。
- 静态、反向代理、PHP 网站的实际 HTTP 内容；上传与编辑；站点停用／启用。
- 故意损坏 Nginx 配置的 fixture 触发失败回退及重启检查；删除站点后保留文件。
- 认证、CSRF 和管理同意撤销检查。

这不是程序 Release 发布，也不是任意生产网站、公网连通、所有业务负载或完整旧机权限迁移验收。站点“启用”也不是业务软件的 `systemctl enable` 自动开机承诺。

### 隔离测试与其余空白

- **Windows PowerShell 本机 57/57**：install 23、maintenance 18、privilege 16，均在 TEMP/mock ACL/SCM 环境；同组 57 项随后也在最新合并 CI 成功。两者均不是真实 LocalSystem、SCM 注册或完整 NTFS ACL 验收。
- **Linux 安装器历史首轮隔离测试 135 项通过，含 10 项 privilege**：普通 `--yes` 不授权、准确确认短语、新装 root、旧安装权限迁移、root 启动失败回退、日志属主／原停止状态恢复，以及已知 v0.6 管理器迁移后仍 restricted；与上述 Ubuntu 真实包／HTTP 测试分开记录。
- **MariaDB／Redis 仅 fake 逻辑测试，未真实安装；DNF 未真实测试。** 不由 Nginx／PHP 或 Ubuntu 的结果推导其通过。
- 网站证书按**所有站点域名**校验，导入仅有单元／loopback 测试，**真实 Nginx HTTPS 尚未验证**；非 ACME。
- **GUI 仅登录截图通过**。后续 screenshot 工具超时，软件／网站／任务的完整 GUI 操作与截图验收未完成，不能因 API／HTTP 测试通过就称 GUI 通过。
- Windows 真实 LocalSystem/ACL、不同 Linux 发行版／架构／SELinux、完整旧管理器迁移与权限回退、物理故障恢复和长期负载仍需独立验收。

本地 Windows 开发主机不执行真实包安装、SCM／PATH／ACL 修改；真实主机操作应仅在获准的专用隔离测试环境内进行，不连接或改动既有业务服务器。后续新 Release 仍须另行确认确切提交；本地文档构建不发布网站或程序。

本轮不提供通用终端、Compose、完整应用商店、任意软件源、数据库管理、任意系统文件编辑、归档展开上传、租户隔离或 ACME。依赖与运行时未知时显示不可用，不用占位数据或假成功冒充已完成。
