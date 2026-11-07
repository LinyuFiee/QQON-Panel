# 发布状态与产物边界

当前已发布预览版 **[v0.7.0 Release](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.7.0)** 提供独立系统管理授权、Linux 软件／网站／任务，以及中文安装、四种访问模式与 `qqon` 维护。当前 `latest` 为 v0.7.0，新安装见 [快速开始](./quickstart)。

::: warning 预览定位与历史能力
预览版不代表稳定成熟或全平台实机验收。**旧 v0.4.0／v0.5.0／v0.6.0 没有真实业务软件安装、网站创建／部署或网站文件编辑**；其“应用与源”配置／模板不是已安装软件。v0.7.0 的真实安装／建站需独立授权，Windows 业务操作仍为 `not_supported`，见 [托管指南](./hosting)。
:::

## 当前已发布：v0.7.0 {#v0-7-preview}

- 发行提交：`5d668b229ae57055cb7667e996e50e58c116abba`。
- [发布 CI #42383705774](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42383705774) 已完成，`test-build`、`windows-test`、`publish` 全部成功，Release 已公开。
- 标题定位为 **QQON-Panel v0.7.0 预览版**；GitHub `prerelease=false` 延续既有 `latest` 通道协议，并不声称产品稳定成熟。
- 本地三平台 `build_release`／`verify-only` 已通过；九个公开资产也已独立下载并通过等价 `verify-only` 校验，资产 SHA、归档／内层摘要和 Windows build-info 的版本、目标提交与时间 `2026-11-07T14:27:21Z` 一致。Windows bootstrap 指定版本及 Latest 的只读元数据解析均确认 v0.7.0、3 平台／8 校验项；已下载发行程序的手动更新检查返回 `up_to_date`、`latest.assetsReady=true`。这些只读／临时回环测试不是实际主机安装验收。
- 发行清单 **format3**、用户数据 schema **1** 不变；安装管理记录升级为 **format3**，增加 `privilegeMode`／`privilegeConsent`。旧 format1/2 默认 restricted，普通 yes/update 不授权。

### 九个发行附件

| 附件 | 内容 |
| --- | --- |
| `panel-linux-amd64.tar.gz` | 单个普通 `panel`，USTAR，模式 `0755` |
| `panel-linux-arm64.tar.gz` | 同上，对应 arm64 |
| `panel-windows-amd64.zip` | 单个普通 `panel.exe`，无目录、链接或加密 |
| `install.sh` / `update.sh` | v0.7.0 官方 Linux 管理器与包装入口 |
| `install.ps1` / `update.ps1` | v0.7.0 官方 Windows 管理器与包装入口 |
| `release.json` | format3；三条记录绑定平台、归档与内层程序大小/摘要 |
| `SHA256SUMS` | 覆盖其余八个附件 |

`qqon` 包装器由安装器生成，不新增发行资产。归档和展开后程序分别限 64 MiB，Linux tar 总展开上限另含 10 KiB 开销；安装器 512 KiB、更新包装脚本 32 KiB、清单 32 KiB、校验清单 16 KiB、API 响应 1 MiB。超限、链接、多成员、路径穿越或身份不匹配均拒绝。

## v0.7.0 验证记录与限制

**发布 CI 与主程序阶段测试分开记录，发行不扩大已经验证的能力。** [主程序阶段 CI #42380757509](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42380757509) 对应 `fa1483f9ec57294d000a6f71014c4e6ee7afe87c`，Linux unit／race／真实托管及 Windows Go＋57 项 PowerShell 成功；它不是发行标签提交的测试身份。

| 验证层次 | 已确认结果 | 不能外推的范围 |
| --- | --- | --- |
| 此前 Ubuntu 真实托管 | [42379665605](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42379665605)，`80fea3b505bc429541ba31ed2f1ac751debaaf9e`；缺包安装／启动 Nginx `1.24.0-2ubuntu7.19`、PHP `8.3.6-0ubuntu0.24.04.13`；静态／代理／PHP HTTP、上传编辑、停用／启用、损坏 Nginx fixture 回退／重启、删除保留文件、认证／CSRF／同意撤销 | 不等于全发行版／架构、完整旧机迁移或真实 Nginx HTTPS |
| Windows 本机与阶段 CI | **57/57**：install 23 + maintenance 18 + privilege 16；TEMP/mock ACL/SCM | 不是真实 LocalSystem／SCM／ACL 安装 |
| Linux 安装器首轮记录 | 135 项隔离测试通过，含 10 项 privilege | 不是完整迁移／物理故障恢复验收 |
| 网站证书导入 | 所有站点域名校验；单元／loopback 测试 | 真实 Nginx HTTPS 未验收，非 ACME |
| GUI | 仅登录截图通过 | 后续截图工具超时，完整 GUI 流程未完成 |

MariaDB／Redis 仅 fake 逻辑测试、未真实安装；DNF 未真实测试。两次各 5 次操作后 process RSS 采样分别为：`80fea3b…`／42379665605 中位数＝最大值 **15.42 MiB**；`fa1483f9…`／42380757509 中位数 **15.51 MiB**、最大值 **15.52 MiB**。不含业务 RAM，不是发行标签的新测量、SLA、soak 或全套负载结论。

公网 URL 是配置结果，不代表 DNS／安全组／防火墙／NAT 或外部可达性已验证。授权、任务和文件边界见 [托管指南](./hosting)，逐平台状态见 [支持矩阵](./support-matrix)。

## 旧版预览发行：v0.6.0 {#当前已发布-v0-6-0}

[v0.6.0 Release](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.6.0) 的 tag 为 `3cfd1a8fe75eca46fd1d991749c1be39f546638c`；[历史发布 CI 42349315659](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42349315659) 的三项任务成功。它提供中文安装、默认公网自签 HTTPS `8443`、四种访问模式与 `qqon`，但没有真实业务软件安装／网站部署；服务使用受限身份，安装管理记录为 format2。

v0.6 的发行清单为 format3 九资产、数据 schema 1。其发布时的更新状态 `up_to_date`／`assetsReady=true`、Windows format3 元数据检查与 Pages HTTPS 200／摘要复核均为历史记录，不作为 v0.7 的当前下载或实机验收证据。

## 旧版预览发行：v0.5.0 {#下一版本-v0-5-待发布}

[v0.5.0 Release](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.5.0) 保留 format3 九资产、schema 1，提供 Linux/Windows 安装，默认回环 `127.0.0.1:5173` 与本机／已有 HTTPS 代理；没有 v0.6 的公网自签默认值或 `qqon` 维护，也没有真实软件安装和建站。

tag 为 `07e3665b582ce68198704fa38180e15f135167d6`，[历史 CI 42328127375](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42328127375) 三项任务成功。九附件 `verify-only`、Windows bootstrap 元数据及本机版本检查保留为 v0.5 历史验证，不移作 v0.7 结果。

## 旧版预览发行：v0.4.0 {#当前已发布-v0-4-0}

[v0.4.0 Release](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.4.0) 使用 format2、schema 1，只有六个 Linux 附件：两个 Linux tar 包、`install.sh`、`update.sh`、`release.json`、`SHA256SUMS`。没有 Windows 产物、真实业务软件安装或网站部署。

固定下载、已知摘要及旧安装方式见 [v0.4.0 指南](./legacy-v0-4)。不覆盖旧附件来追加功能。三个旧版的预览标题／说明修正没有移动原标签或改变资产、发布时间及平台发布标志；当前 `latest` 随 v0.7.0 发布更新。

## Pages 入口与 Release 安装器

- Pages 的 `install.sh`／`install.ps1` 是文档构建时复制的 **bootstrap**；Release 同名附件是匹配该版本协议的完整管理器。
- 入口只选固定项目的已发布、兼容 Release，不从可变 `main`、任意 URL 或镜像替换安装器。下载后先校验再执行。
- 发布文档不会创建程序 tag／Release，也不会自动替换已安装主机上的旧管理器。旧 v0.6 附件不会自动获得 v0.7 参数。

## 更新兼容性

新读取器可识别经验证的旧 format2；v0.4 六资产更新器不能消费 v0.5/v0.6/v0.7 的 format3 九资产。旧安装先**下载、校验并审阅 v0.7.0 管理器 → 显式迁移 → `qqon update` 升级程序、受限运行并确认健康 → 如有需要独立 `qqon privilege`**。Linux 只接管已知 v0.4/v0.5/v0.6，Windows 只接管 v0.5/v0.6，无 Windows v0.4。

普通 yes/update 不授权、不自动公开旧访问、不降级或跨不兼容 schema；完整命令见 [升级与迁移](./upgrade-migration#manager-migration)。

## 发布确认与后续维护 {#什么时候才能改成-已发布}

v0.7.0 按指定提交发布；标题的“预览版”、GitHub `prerelease=false` 和 `latest` 协议是不同概念，不构成成熟度保证。所有已公开 tag 和资产不可变，修复使用另行确认的新版本，不重打或移动旧标签。

文档部署与 Release 独立；本地构建或 Pages workflow 成功不能代替程序发布和真实系统验证。下一版本仍需确切提交的发布授权及完整资产核验，不构造未经确认的下载目标。

## 构建工具不属于服务器依赖

源码／发行构建需 **Go 1.26+**；Windows 原生能力使用官方 `golang.org/x/sys`。安装已发布程序的用户服务器不需 Go、Node 或 npm。

文档使用 Node/npm 与 VitePress 1.6.4，依赖仅在 `docs`。完整 `npm audit` 记录 **3 项（2 moderate、1 high）**，不能称无漏洞，见 [文档构建边界](./docs-development)。
