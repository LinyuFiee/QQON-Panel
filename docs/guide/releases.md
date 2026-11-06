# 发布状态与产物边界

::: warning 当前 v0.4.0 · v0.5 尚未发布
本页记录的可用正式版本是 **v0.4.0**。交互入口、RPM 适配、Windows 服务/原生指标和 format3 属于下一版本；源码提交、CI 成功、文档更新或 Pages 部署都不能作为它们已经发布的证明。没有兼容 Release 时，新入口应明确拒绝继续。
:::

## 当前已发布：v0.4.0

[v0.4.0 正式 Release](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.4.0) 使用清单 **format2**、数据 schema **1**，包含六个附件：

| 附件 | 内容 |
| --- | --- |
| `panel-linux-amd64.tar.gz` | Linux amd64 程序，包内仅普通 `panel` |
| `panel-linux-arm64.tar.gz` | Linux arm64 程序，包内仅普通 `panel` |
| `install.sh` | 该版本 Linux 安装/升级管理器 |
| `update.sh` | Linux 本地升级包装入口 |
| `release.json` | 版本、仓库、提交、数据格式、大小与摘要 |
| `SHA256SUMS` | 发布附件的 SHA-256 校验清单 |

当前安装路径见 [v0.4.0 指南](./legacy-v0-4)。旧产物保持不变，不通过覆盖 v0.4.0 附件来追加 Windows 支持。

## 下一版本：v0.5 待发布

目标协议 **format3** 保持数据 schema **1**，完整 Release 必须包含以下九个附件。下表是格式约定，**不是可下载链接，也不表示附件已经存在**。

| 附件 | 约定内容 |
| --- | --- |
| `panel-linux-amd64.tar.gz` | 单个普通 `panel`，USTAR，模式 `0755` |
| `panel-linux-arm64.tar.gz` | 同上，对应 arm64 |
| `panel-windows-amd64.zip` | 单个普通 `panel.exe`，无目录、链接或加密 |
| `install.sh` / `update.sh` | 正式 Linux 管理器与包装入口 |
| `install.ps1` / `update.ps1` | 正式 Windows 管理器与包装入口 |
| `release.json` | format3；三条程序记录分别绑定平台、归档与内层程序大小/摘要 |
| `SHA256SUMS` | 覆盖其余八个附件 |

归档和展开后的程序分别限制为 64 MiB，Linux tar 总展开上限另含 10 KiB 归档开销。安装器上限 512 KiB、更新包装脚本 32 KiB、清单 32 KiB、校验清单 16 KiB、API 响应 1 MiB；超限、链接、多成员、路径穿越或身份不匹配均应拒绝。校验规则不是“能解压就行”。

v0.5 目标功能和真实平台验收要分开看，参见 [支持矩阵](./support-matrix)。本页不声明所有 Windows/RPM 版本已完成实机验证。

## Pages 入口与 Release 安装器

虽然 URL 同名，但分发层次不同：

- `https://linyufiee.github.io/QQON-Panel/install.sh` / `install.ps1` 是构建时复制的 **bootstrap**。
- Release 附件 `install.sh` / `install.ps1` 是与该版本协议匹配的完整管理器。
- 正式入口固定从项目稳定 Release 取管理器；不存在兼容 format3 时失败，不使用可变 `main` 源码冒充正式产物。
- 发布文档不会创建 tag 或 Release，也不会自动替换用户已经审阅的本地管理器。

## 更新兼容性

新读者可读取经验证的 format2 作为旧版信息；新安装/写入使用 format3。v0.4.0 严格六附件的更新器不能消费 format3，必须进行 [显式管理器迁移](./upgrade-migration#manager-migration)。普通更新仍拒绝降级、不兼容数据格式和任意来源脚本。

版本号采用稳定的 `v主.次.修订` 格式；预发布标签不能被当作稳定通道保证。需要查看公开状态时使用 [Release 列表](https://github.com/LinyuFiee/QQON-Panel/releases)，不要构造不存在版本的下载链接。

## 什么时候才能改成“已发布”

维护者需要核对具体提交、CI/打包校验结果、完整附件及其实际可下载性，并确认针对哪些目标系统完成了实机验收，再同步更新首页和相关页面。新 tag/Release 需要针对确切提交的单独发布确认，不能因为 Pages workflow 成功而自动发布程序。

## 构建工具不属于用户服务器依赖

- 下一版本源码与发行构建需要 **Go 1.26+**；Windows 原生能力使用官方 `golang.org/x/sys`。
- 本文档构建使用 Node.js/npm 与 VitePress，依赖只放在 `docs` 下。
- 安装已发布程序的用户服务器不需要 Go、Node 或 npm。不要把开发工具要求写成面板运行要求。
