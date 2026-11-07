# 发布状态与产物边界

当前已发布预览版 **[v0.6.0 Release](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.6.0)** 提供中文安装、默认公网 IPv4 自签 HTTPS `8443`、四种访问模式和 `qqon` 维护。当前下载仍指向 v0.6.0，新安装使用 [快速开始](./quickstart)。

::: warning 预览定位与未发布能力
**v0.4.0、v0.5.0、v0.6.0 均为预览版**，保留官方已发布事实，不代表稳定成熟或全平台实机验收。三个版本均没有真实业务软件安装、网站创建／部署或网站文件编辑；“应用与源”配置／模板不等于软件已经安装。

**v0.7 预览开发中，尚未发布；已有有限测试结果，不代表全平台验收。** 新的最高权限授权、Linux 软件／网站／任务流程及验证范围见 [托管开发指南](./hosting)，不要将这些开发接口套用到当前下载，也不要构造 v0.7 Release／附件链接。
:::

维护者已将历史 GitHub Release v0.4.0／v0.5.0／v0.6.0 的标题与说明修正为预览定位，并核对原 tag／目标提交、资产 ID／大小／摘要、发布时间及 draft／prerelease 标志未变，`latest` 仍为 v0.6.0。文字标识修正不新增或覆盖发行资产，也不表示 v0.7 已发布。

## 当前已发布：v0.6.0

- 官方已发布 tag 指向提交 `3cfd1a8fe75eca46fd1d991749c1be39f546638c`。
- [发布 CI #42349315659](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42349315659) 的 `test-build`、`windows-test`、`publish` 均为 `success`。
- Windows 新增 maintenance 隔离测试已在此 CI 通过；不是宿主真实 SCM 安装测试。
- v0.6.0 发布时的只读记录：面板更新检查显示 v0.6.0、`state=up_to_date`、`assetsReady=true`；Windows bootstrap 的 `Get-QPReleaseMetadata` 通过真实 format3 三平台元数据检查。
- 同期 Pages 两个 bootstrap 的 HTTPS 响应均为 200，文件摘要与当时源码一致。这些是历史发布记录，不是当前线上重新检查、真实服务安装或 v0.7 验收。

v0.6.0 发行协议为 **format3**，数据 schema 为 **1**；安装管理记录为 **format2**，不要混淆三者。官方 Release 保持九个附件，`qqon` 包装入口由安装器生成，不新增发布资产：

| 附件 | 内容 |
| --- | --- |
| `panel-linux-amd64.tar.gz` | 单个普通 `panel`，USTAR，模式 `0755` |
| `panel-linux-arm64.tar.gz` | 同上，对应 arm64 |
| `panel-windows-amd64.zip` | 单个普通 `panel.exe`，无目录、链接或加密 |
| `install.sh` / `update.sh` | 该版本官方 Linux 管理器与包装入口 |
| `install.ps1` / `update.ps1` | 该版本官方 Windows 管理器与包装入口 |
| `release.json` | format3；三条程序记录分别绑定平台、归档与内层程序大小/摘要 |
| `SHA256SUMS` | 覆盖其余八个附件 |

归档和展开后的程序分别限制为 64 MiB，Linux tar 总展开上限另含 10 KiB 归档开销。安装器上限 512 KiB、更新包装脚本 32 KiB、清单 32 KiB、校验清单 16 KiB、API 响应 1 MiB；超限、链接、多成员、路径穿越或身份不匹配均应拒绝。校验规则不是“能解压就行”。

**发布和 CI 通过不等于真实安装验收。** 具体系统版本安装、真实 SCM/ACL、APT/DNF、SELinux 和物理故障恢复仍未验证，参见 [支持矩阵](./support-matrix)。公网 URL 仅为配置结果，未检测外网可达性；自签警告、证书指纹及云安全组/防火墙/NAT 边界见 [公网 HTTPS](./public-https)。

## 旧版预览发行：v0.5.0 {#下一版本-v0-5-待发布}

[v0.5.0 官方预览版 Release](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.5.0) 保留 format3 九附件、数据 schema 1，提供 Linux/Windows 安装，默认回环 `127.0.0.1:5173` 与本机／已有 HTTPS 代理访问；没有 v0.6 的公网自签默认配置和 `qqon` 维护入口，也没有真实业务软件安装和建站。

其 tag 为 `07e3665b582ce68198704fa38180e15f135167d6`，[旧版 CI #42328127375](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42328127375) 的三项任务均成功。该版本九附件重新下载通过 `verify-only`、Windows bootstrap 只读元数据检查通过、本地面板版本检查 `state=up_to_date` / `assetsReady=true`，均为 **v0.5.0 历史记录**，不是 v0.6.0 或 v0.7 的额外实机验收。

## 旧版预览发行：v0.4.0 {#当前已发布-v0-4-0}

[v0.4.0 官方预览版 Release](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.4.0) 使用清单 **format2**、数据 schema **1**，保留六个附件；没有真实业务软件安装与网站部署：

| 附件 | 内容 |
| --- | --- |
| `panel-linux-amd64.tar.gz` | Linux amd64 程序，包内仅普通 `panel` |
| `panel-linux-arm64.tar.gz` | Linux arm64 程序，包内仅普通 `panel` |
| `install.sh` | 该版本 Linux 安装/升级管理器 |
| `update.sh` | Linux 本地升级包装入口 |
| `release.json` | 版本、仓库、提交、数据格式、大小与摘要 |
| `SHA256SUMS` | 发布附件的 SHA-256 校验清单 |

固定旧版安装路径见 [v0.4.0 指南](./legacy-v0-4)。旧产物保持不变，不通过覆盖 v0.4.0 附件来追加 Windows 支持。

## v0.7 预览开发中 {#v0-7-preview}

本轮开发范围为独立同意后的 Linux root／Windows LocalSystem 服务身份、Linux 白名单软件安装与服务动作、Nginx 静态／代理／PHP 网站、有限文件上传编辑、已有证书导入和共享后台任务。Windows 不支持这些 Linux 软件与建站功能；软件源模板不替代主机现有可信 APT/DNF 源，TLS 导入不是 ACME，面板自身内存不包含业务软件内存。

**尚无已发布 v0.7 程序，以下是按环境区分的有限验证记录。**

最新全合并代码 [CI #42380757509](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42380757509) 已成功，提交 `fa1483f9ec57294d000a6f71014c4e6ee7afe87c`；Linux unit／race／真实托管及 Windows Go＋57 项 PowerShell 全部成功，Windows CI 仍是隔离测试而非真实 SCM／LocalSystem 安装；下述 `80fea3b…` 的 15.42 MiB 保留为前一次独立测量，不移作本次结果。

| 验证层次 | 已确认结果 | 不能外推的范围 |
| --- | --- | --- |
| Ubuntu 真实托管 CI | [run #42379665605](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42379665605) 成功，提交 `80fea3b505bc429541ba31ed2f1ac751debaaf9e`；缺包安装并启动 Nginx `1.24.0-2ubuntu7.19`、PHP `8.3.6-0ubuntu0.24.04.13`；静态／代理／PHP HTTP、上传编辑、站点停用／启用、损坏 Nginx 配置 fixture 回退与重启、删除保留文件、认证／CSRF／同意撤销 | 不代表所有 Linux 发行版／架构、DNF、完整旧机迁移或真实 Nginx HTTPS 通过 |
| Windows PowerShell 本机及后续 CI | 本机 **57/57**：install 23 + maintenance 18 + privilege 16；同组 57 项在最新合并 CI 也成功，仍为 TEMP/mock ACL/SCM | 不是真实 LocalSystem／SCM／ACL 安装验收 |
| Linux 安装器首轮隔离记录 | 历史 **135 项通过，含 10 项 privilege** | 不与真实主机测试混计，不能替代完整迁移／故障恢复 |
| 网站证书导入 | 校验全部站点域名；单元／loopback 测试 | **尚未真实 Nginx HTTPS 验收**，也不是 ACME |
| GUI | 仅登录截图通过 | 后续 screenshot 工具超时，整体 GUI 流程未完成 |

MariaDB／Redis 仅 fake 逻辑测试，未真实安装；DNF 未真实测试。前一次提交 `80fea3b505bc429541ba31ed2f1ac751debaaf9e` 对应 Ubuntu CI 的 5 次操作后短时采样中，面板 RSS 中位数与最大值均为 **15.42 MiB**，不含业务进程，不是 SLA、soak 或全套负载结论；不将该数据归到后续合并 CI。

Linux／Windows 授权命令、新装与迁移前提见 [托管开发指南](./hosting#authorization)；完整验证边界见 [支持矩阵](./support-matrix#hosting-preview)。旧 v0.6 发布 CI 不作本轮证据，单个 Ubuntu runner 也不代表全平台验收。

开发约定中，发行清单仍为 format3 九资产、数据 schema 仍为1；**安装管理记录**升级为 format3，增加 `privilegeMode`／`privilegeConsent`，与发行清单同名格式号无关。旧 format1/2 默认 restricted，普通 yes/update 不授权；另见 [权限与迁移](./hosting#authorization)。

## Pages 入口与 Release 安装器

虽然 URL 同名，但分发层次不同：

- `https://linyufiee.github.io/QQON-Panel/install.sh` / `install.ps1` 是构建时复制的 **bootstrap**。
- Release 附件 `install.sh` / `install.ps1` 是与该版本协议匹配的完整管理器。
- 官方入口从项目已发布、符合当前通道与格式的 Release 取管理器；不存在兼容格式时失败，不使用可变 `main` 源码冒充已发布产物。新文档不能使 v0.6 附件自动获得 v0.7 参数。
- 发布文档不会创建 tag 或 Release，也不会自动替换用户已经审阅的本地管理器。

## 更新兼容性

新读取器可读取经验证的 format2 作为旧版发行信息；v0.5/v0.6 发行清单使用 format3。v0.4.0 严格六附件的更新器不能消费 format3。已有 v0.4/v0.5 升级 v0.6.0 时，先 [显式迁移管理器](./upgrade-migration#manager-migration) 并保留旧访问，再升级程序，最后按需使用 `qqon config` 切换公网。普通更新仍拒绝降级、不兼容数据格式和任意来源脚本，也不授予最高系统管理权限。

版本号使用 `v主.次.修订` 格式；GitHub `latest`、是否带预发布后缀以及 Release 的平台标志不代表产品成熟度。当前 v0.4/v0.5/v0.6 的预览定位不要求移动既有 tag 或改写资产。需要查看公开状态时使用 [Release 列表](https://github.com/LinyuFiee/QQON-Panel/releases)，不要构造不存在版本的下载链接。

## 发布确认与后续维护 {#什么时候才能改成-已发布}

v0.6.0 已按上述官方 tag、CI 与公开 Release 记录为已发布预览版；未覆盖的系统安装与恢复验收仍须单独记录。**后续新 tag/Release 需要针对确切提交的发布确认**，核对完整附件及实际可下载性后再同步更新文档。v0.7 目前没有新发行确认；Pages workflow 成功不会自动发布程序，也不能替代真实系统验证。

## 构建工具不属于用户服务器依赖

- v0.6.0 源码与发行构建需要 **Go 1.26+**；Windows 原生能力使用官方 `golang.org/x/sys`。
- 本文档构建使用 Node.js/npm 与 VitePress，依赖只放在 `docs` 下。VitePress 保持 1.6.4；已记录完整 `npm audit` 为 **3 项（2 moderate、1 high）**，不能称为无漏洞，见 [文档构建边界](./docs-development)。
- 安装已发布程序的用户服务器不需要 Go、Node 或 npm。不要把开发工具要求写成面板运行要求。
