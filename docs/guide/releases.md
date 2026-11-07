# 发布状态与产物边界

当前稳定版 **[v0.6.0 正式 Release](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.6.0) 已发布**，提供中文安装、默认公网 IPv4 自签 HTTPS `8443`、四种访问模式和 `qqon` 维护。新安装使用 [快速开始](./quickstart)。

## 当前已发布：v0.6.0

- 正式 tag 指向提交 `3cfd1a8fe75eca46fd1d991749c1be39f546638c`。
- [发布 CI #42349315659](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42349315659) 的 `test-build`、`windows-test`、`publish` 均为 `success`。
- Windows 新增 maintenance 隔离测试已在此 CI 通过；不是宿主真实 SCM 安装测试。
- 面板实际更新检查显示当前 v0.6.0、`state=up_to_date`、`assetsReady=true`；Windows bootstrap 的 `Get-QPReleaseMetadata` 已通过真实 format3 三平台元数据检查。
- Pages 两个 bootstrap 的 HTTPS 响应均为 200，文件摘要与源码一致。这些发布及只读检查不代表已执行真实服务安装或完成外网连通验收。

发行协议仍为 **format3**，数据 schema 为 **1**；安装管理记录为 **format2**，不要混淆三者。正式 Release 保持九个附件，`qqon` 包装入口由安装器生成，不新增发布资产：

| 附件 | 内容 |
| --- | --- |
| `panel-linux-amd64.tar.gz` | 单个普通 `panel`，USTAR，模式 `0755` |
| `panel-linux-arm64.tar.gz` | 同上，对应 arm64 |
| `panel-windows-amd64.zip` | 单个普通 `panel.exe`，无目录、链接或加密 |
| `install.sh` / `update.sh` | 正式 Linux 管理器与包装入口 |
| `install.ps1` / `update.ps1` | 正式 Windows 管理器与包装入口 |
| `release.json` | format3；三条程序记录分别绑定平台、归档与内层程序大小/摘要 |
| `SHA256SUMS` | 覆盖其余八个附件 |

归档和展开后的程序分别限制为 64 MiB，Linux tar 总展开上限另含 10 KiB 归档开销。安装器上限 512 KiB、更新包装脚本 32 KiB、清单 32 KiB、校验清单 16 KiB、API 响应 1 MiB；超限、链接、多成员、路径穿越或身份不匹配均应拒绝。校验规则不是“能解压就行”。

**发布和 CI 通过不等于真实安装验收。** 具体系统版本安装、真实 SCM/ACL、APT/DNF、SELinux 和物理故障恢复仍未验证，参见 [支持矩阵](./support-matrix)。公网 URL 仅为配置结果，未检测外网可达性；自签警告、证书指纹及云安全组/防火墙/NAT 边界见 [公网 HTTPS](./public-https)。

## 旧版发行：v0.5.0 {#下一版本-v0-5-待发布}

[v0.5.0 正式 Release](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.5.0) 保留 format3 九附件、数据 schema 1，提供 Linux/Windows 安装，默认回环 `127.0.0.1:5173` 与本机／已有 HTTPS 代理访问；没有 v0.6 的公网自签默认配置和 `qqon` 维护入口。

其 tag 为 `07e3665b582ce68198704fa38180e15f135167d6`，[旧版 CI #42328127375](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42328127375) 的三项任务均成功。该版本九附件重新下载通过 `verify-only`、Windows bootstrap 只读元数据检查通过、本地面板版本检查 `state=up_to_date` / `assetsReady=true`，均为 **v0.5.0 历史记录**，不是本次 v0.6.0 的额外实机验收。

## 旧版发行：v0.4.0 {#当前已发布-v0-4-0}

[v0.4.0 正式 Release](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.4.0) 使用清单 **format2**、数据 schema **1**，保留六个附件：

| 附件 | 内容 |
| --- | --- |
| `panel-linux-amd64.tar.gz` | Linux amd64 程序，包内仅普通 `panel` |
| `panel-linux-arm64.tar.gz` | Linux arm64 程序，包内仅普通 `panel` |
| `install.sh` | 该版本 Linux 安装/升级管理器 |
| `update.sh` | Linux 本地升级包装入口 |
| `release.json` | 版本、仓库、提交、数据格式、大小与摘要 |
| `SHA256SUMS` | 发布附件的 SHA-256 校验清单 |

固定旧版安装路径见 [v0.4.0 指南](./legacy-v0-4)。旧产物保持不变，不通过覆盖 v0.4.0 附件来追加 Windows 支持。

## Pages 入口与 Release 安装器

虽然 URL 同名，但分发层次不同：

- `https://linyufiee.github.io/QQON-Panel/install.sh` / `install.ps1` 是构建时复制的 **bootstrap**。
- Release 附件 `install.sh` / `install.ps1` 是与该版本协议匹配的完整管理器。
- 正式入口固定从项目稳定 Release 取管理器；不存在兼容格式时失败，不使用可变 `main` 源码冒充正式产物。
- 发布文档不会创建 tag 或 Release，也不会自动替换用户已经审阅的本地管理器。

## 更新兼容性

新读取器可读取经验证的 format2 作为旧版发行信息；新安装/写入使用 format3。v0.4.0 严格六附件的更新器不能消费 format3。已有 v0.4/v0.5 升级 v0.6.0 时，先 [显式迁移管理器](./upgrade-migration#manager-migration) 并保留旧访问，再升级程序，最后按需使用 `qqon config` 切换公网。普通更新仍拒绝降级、不兼容数据格式和任意来源脚本。

版本号采用稳定的 `v主.次.修订` 格式；预发布标签不能被当作稳定通道保证。需要查看公开状态时使用 [Release 列表](https://github.com/LinyuFiee/QQON-Panel/releases)，不要构造不存在版本的下载链接。

## 发布确认与后续维护 {#什么时候才能改成-已发布}

v0.6.0 已按上述正式 tag、CI 与公开 Release 记录为已发布；未覆盖的系统安装与恢复验收仍须单独记录。后续新 tag/Release 需要针对确切提交的发布确认，核对完整附件及实际可下载性后再同步更新文档。Pages workflow 成功不会自动发布程序，也不能替代真实系统验证。

## 构建工具不属于用户服务器依赖

- v0.6.0 源码与发行构建需要 **Go 1.26+**；Windows 原生能力使用官方 `golang.org/x/sys`。
- 本文档构建使用 Node.js/npm 与 VitePress，依赖只放在 `docs` 下。
- 安装已发布程序的用户服务器不需要 Go、Node 或 npm。不要把开发工具要求写成面板运行要求。
