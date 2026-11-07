# 升级与迁移

::: info v0.7.0 预览版已发布 · 旧版需显式迁移
[v0.7.0 官方预览版 Release](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.7.0) 提供独立系统管理授权与 Linux 软件／网站。旧安装先校验、审阅并迁移 v0.7.0 管理器，再 `qqon update`、保持受限并确认健康，最后按需独立 `qqon privilege`；不重置账号、不自动开放旧访问。发布不等于全平台恢复验收，见 [支持矩阵](./support-matrix)。
:::

## 四种操作不要混淆

| 操作 | 作用 | 不能承诺的事 |
| --- | --- | --- |
| 普通程序升级 | 在兼容的管理器与数据格式下切换到已发布的新程序 | 不会自动替换安装器、切换公网、授予 root/LocalSystem、降级或做任意数据迁移 |
| 管理器迁移 | 显式更新已知旧安装器/配置，使其理解新安装记录与维护能力 | 不会自动接管手动安装或被修改的脚本，也不代替最高权限同意 |
| 最高权限迁移（v0.7.0） | 独立同意后改变服务身份和相关私有权限／记录 | 普通 yes/update、root 或管理员身份本身不构成授权；有限测试不是完整迁移验收 |
| 数据迁移 | 数据结构变更时的独立计划 | 不属于当前 schema 1 普通升级/回退承诺 |

发行清单为 format3，数据 schema 为1；v0.7.0 安装管理记录也为 format3，旧 v0.6 安装记录为 format2，它们不是同一个文件。网页不能授予 root／LocalSystem 同意或执行任意命令；只有独立授权后服务才以最高身份运行。重启使内存会话失效，账号、TOTP 和私有配置保留。

## v0.4.0 现有安装

v0.4.0 管理器验证 format2 与六个固定附件，不能直接消费 v0.5/v0.6/v0.7 的 format3 九附件。保留旧版无需重复安装，升级则按下节显式迁移管理器；不能删校验或伪造六附件清单来升级。

旧 v0.4.0 默认回环 `127.0.0.1:5173`；v0.5.0 仍固定回环，允许选择端口及已有 HTTPS 代理。v0.6 的公网自签默认值只用于新装，不自动改变这些旧安装的访问方式。

## 旧版 → v0.7.0 管理器迁移 {#manager-migration}

从 [v0.7.0 官方 Release](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.7.0) 获取完整管理器，核对同版 `SHA256SUMS`、大小和来源，审阅后运行。不能用可变 `main` 脚本代替发行来源；`--yes`／`-Yes` 不隐式批准更换管理器或最高权限，仍需相应独立同意。

以下摘要仅对应历史 v0.4.0 文件，不作为 v0.5 或新管理器摘要：

| 文件 | 官方 SHA-256 |
| --- | --- |
| v0.4.0 `install.sh` | `bc78f2ec682704bb5beb965806ec56e511827f6d3451949a03d5b40db535eb91` |
| 对应 `update.sh` 包装入口 | `5cdc4f05cce7189b7212e5b12496e0754985c45c1462fc2ce0aa1a86f81b1408` |

文件哈希只是识别条件之一，还需核对旧安装记录、程序身份与 unit/服务，不能仅信任可变配置里写的摘要。

迁移顺序：

1. 管理员先停服取得一致私有数据备份，确认旧安装属于可识别的官方版本。
2. 若旧升级事务未结束，先按已知旧管理器恢复；不要跳过旧事务。
3. 新安装器解释差异并取得显式确认，再备份、记录迁移事务；保留原服务身份、端口、访问模式和数据。
4. 完成管理器迁移后结束该操作，再单独升级程序；先确认新程序在原访问方式下健康。
5. 需要公网 HTTPS 时，再运行 `qqon config` 确认公网 IPv4、端口与证书配置。**先新程序、后新公网配置**，不能把新配置交给旧程序。

手工复制的二进制、format1 开发安装、未知或修改过的脚本不在自动迁移范围。拒绝迁移时应停止并制定独立迁移方案，不使用“强制接管”或伪造安装记录。

## v0.4 / v0.5 / v0.6 → v0.7.0 授权迁移 {#privilege-migration}

使用已校验的 v0.7.0 Release 管理器，不用可变 `main` 或未经校验的脚本。预览发布和有限 CI 不构成完整旧机迁移／回退保证；先取得可恢复的私有备份。

### Linux

1. 核对旧版本来源、管理器固定摘要、安装记录、真实服务身份和未完成事务；只迁移被识别的官方 Linux v0.4/v0.5/v0.6，未知或修改过的管理器停止接管。
2. 通过匹配版本的新管理器独立迁移管理记录与维护入口，保留访问、账号和原受限身份；普通更新不能当作最高权限授权。
3. Linux 通过 `sudo qqon update` 单独升级到 v0.7.0，**仍为 restricted**，在原访问配置和受限身份下确认健康。阶段隔离测试和 Ubuntu 托管 CI 不替代完整旧机权限迁移／回退验收。
4. 确需系统管理时，再通过 `sudo qqon privilege` 独立同意，交互输入 `同意最高权限`；已阅读告知后可用 `sudo qqon privilege --accept-system-management --yes` 明确授权。单独 `--yes` 不授权；普通 `--upgrade`／`--update`／`--status`／`--migrate-manager` 均拒绝夹带授权标志。
5. 授权迁移须停服取得一致备份，用事务保护服务身份、数据所有权/ACL、固定同意记录和安装配置；失败恢复原状态并保留诊断，不重置账号。授权后还须确认实际 root／LocalSystem 与同意状态，不能只看命令退出码。

### Windows v0.5 / v0.6 → v0.7 {#windows-v07-migration}

**没有 Windows v0.4 安装或迁移支持。** 仅识别官方 v0.5/v0.6，未知或改写脚本拒绝接管。下面的 `install.ps1` 必须是从 v0.7.0 Release 下载、校验并审阅的完整管理器，不是旧本地脚本或可变分支版本。

1. 在管理员 PowerShell 中，先独立迁移管理器，保留原访问、账号和受限身份：

   ```powershell
   & .\install.ps1 -Action Migrate -Version v0.7.0 -MigrateLegacy -Yes
   ```

   `-MigrateLegacy` 明确同意的是旧管理器迁移，`-Yes` 仅确认普通操作；不在此步骤附加 `-AcceptSystemManagement`，不把迁移当作提权。

2. 确认管理器迁移完成后，单独运行 `qqon update`，先核对新程序在原访问方式和受限身份下健康。
3. 确需 LocalSystem 时，才运行 `qqon privilege` 并输入 `同意最高权限`，或已阅读告知后显式执行：

   ```powershell
   qqon privilege --accept-system-management --yes
   ```

   没有 PATH 入口时，使用[带引号的完整 PowerShell 管理器调用](./windows-services#privilege-commands)。授权迁移的停服备份、ACL／服务身份恢复和同意记录保护不能省略。

Windows 本机 **57/57（install 23、maintenance 18、privilege 16）** 使用 TEMP/mock ACL/SCM，同组测试随后也在最新合并 CI 通过；这些只证明相应隔离断言，不是真实 LocalSystem／SCM 安装，真实权限迁移／回退仍需专用机器验证。

v0.7 安装管理记录的 format3 新增 `privilegeMode`／`privilegeConsent`；旧 format1/2 默认 restricted，不因普通升级或配置操作变为 system。Windows 的 LocalSystem 授权不意味着支持 Linux 软件／网站。更多限制见 [托管指南](./hosting#authorization)。

## 普通升级的验证顺序

迁移完成后的 v0.7.0 管理器使用 `qqon update`，兼容的旧更新入口仍保留。Linux 和 Windows 都先持锁，再核对固定仓库的 Release 元数据、尺寸、SHA-256、唯一归档成员、目标平台、程序内嵌版本和数据 schema。下载与校验完成前不停止旧服务。

通过校验后，在程序文件系统暂存和保留旧程序，落盘事务，再停止服务、切换、启动，核对本机健康接口返回的版本与 schema。Windows 还必须等待进程退出和文件解锁；HTTPS 探针不能通过忽略证书校验假报成功。不能仅凭下载、解压或服务命令返回成功判断升级成功。

## 回退与恢复

- 普通升级拒绝降级或不同数据 schema。当前数据 schema 为 `1`。
- 回退针对程序，不重置管理员、TOTP、数据目录或账号锁文件。
- 新版启动/健康校验失败时尝试恢复旧程序；恢复失败必须保留错误与事务，不报告成功。
- Linux 旧格式事务使用 `prepared` / `ready` 状态；未结束时按相应管理器先校验并恢复，不把恢复当作自动重试新版本。
- `qqon config` 与 `qqon tls renew` 也采用事务并保留旧配置/证书，不能删记录绕过失败。
- 断电、磁盘故障或权限破坏可能阻止恢复；临时目录测试不是灾难恢复保证。

保留 `/etc/server-panel/update-transaction.json`（Linux）或 Windows 对应受保护事务及旧程序备份。不要删除事务、锁或私有目录来“清空状态”。遇到失败按 [故障排查](./troubleshooting) 收集必要的非敏感信息；备份与配置详见 [qqon 维护](./maintenance)。
