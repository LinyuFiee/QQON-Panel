# 发布与升级指南

目标仓库为 [LinyuFiee/QQON-Panel](https://github.com/LinyuFiee/QQON-Panel)。以下发布步骤面向维护者；示例代理域名和本地路径需替换为实际值。CI 从仓库上下文与标签自动注入更新来源，安装器无需硬编码个人凭据。

## 1. 首次上传前

1. 创建一个公开 GitHub 仓库，启用 Actions。
2. 检查待上传文件，只上传源代码、测试、脚本与文档。不要上传 `.zcode`、本机数据目录、初始化令牌、账户 JSON、浏览器会话、私钥、QA 密码或未审查的二进制。
3. `.gitignore` 已排除本地构建、临时数据和测试输出；仍需人工检查暂存内容。
4. 按 README 在测试机运行测试；正式发布前在独立 Debian / Ubuntu 机器完成安装和升级回退验证。

首次上传和后续提交均应先检查 `git diff --cached` 与文件清单；避免使用未经检查的 `git add .` 把本机数据一并提交。

## 2. 发布一个版本

把代码提交并推送到自己的仓库后，给已经测试过的提交创建稳定版标签。格式仅支持 `v主版本.次版本.修订号`，数字不能有前导零，例如：

```sh
git tag -a v0.4.0 -m "Release v0.4.0"
git push origin v0.4.0
```

推送标签会触发 `.github/workflows/release.yml`：

1. 校验标签、仓库、事件 SHA 与实际提交一致。
2. 运行 Python 安装／打包测试、Bash 语法检查、Go 单测、静态检查和 Linux 竞态检测。
3. 以 CGO_DISABLED 构建 Linux amd64、arm64 单文件程序，写入同一组版本、提交、UTC 构建时间和仓库信息。
4. 将程序封装为可重现的单成员 USTAR + gzip 压缩包，生成并复核 format2 `release.json` 与 `SHA256SUMS`；同时记录压缩包和内层 ELF 的独立大小／摘要。
5. 只读构建任务上传固定六个文件；具有 `contents: write` 的发布任务下载后再次校验，不执行下载产物。
6. 重新核对远端标签指向，创建草稿 Release、上传产物、确认完整，再发布为 Latest。

预发布标签（如 `v0.4.1-rc.1`）和非法版本会失败，不进入稳定更新通道。当前普通升级仅允许数据格式 `1` 且版本上升；需要数据迁移时必须先设计单独迁移与恢复流程。

### 每个 Release 的文件

| 文件 | 用途 |
| --- | --- |
| `panel-linux-amd64.tar.gz` | x86_64 Linux 压缩包，唯一成员 `panel` |
| `panel-linux-arm64.tar.gz` | aarch64 Linux 压缩包，唯一成员 `panel` |
| `install.sh` | 用户下载并审阅后执行的自包含安装器 |
| `update.sh` | 受控升级入口包装脚本 |
| `release.json` | 版本、提交、仓库、数据格式和程序大小／摘要 |
| `SHA256SUMS` | 其余五个文件的 SHA-256 校验清单 |

Go 源码包和 GitHub 自动生成的 source archives 不作为面板升级安装对象。

## 3. 安装者怎么用

从明确版本的 Release 下载 `install.sh`，先审查内容，再在受支持的 Linux 服务器执行：

```sh
sudo bash install.sh --repo LinyuFiee/QQON-Panel
```

指定首次安装版本或面板 HTTPS 代理来源：

```sh
sudo bash install.sh --repo LinyuFiee/QQON-Panel --version v0.4.0 --origin https://panel.example.com
```

安装器先检查环境，必要时经确认通过现有 APT 源补齐 Python、CA 证书和必要工具，再下载对应架构压缩包并安全解压。它不会配置证书或反向代理；默认仅监听本机。账号没有默认密码，首次仍使用本机引导令牌初始化。

发布后续版本（例如 `v0.4.1`）以后，用户在面板中手动检查更新，并在已经安装的 Linux 服务器执行：

```sh
sudo panel-update
# 或指定新版本
sudo panel-update --version v0.4.1
```

更新不经过网页提权。脚本在停服前验证程序，保留上一个版本，并在新版本启动失败时尝试回退。正常升级保留账户与配置；升级后的内存会话会失效，需要重新登录。

**升级器本身不会随 Release 自动下载执行新脚本。** 如果未来安装协议或升级器需要变更，要明确发布人工迁移步骤，让管理员重新审阅可信脚本，不能假设旧安装器自动学会新协议。

## 4. 本地打包

需要 Go 1.25+、Python 3、真实仓库标识和真实完整提交 SHA：

```sh
python3 -I tools/build_release.py \
  --version v0.4.0 \
  --repo LinyuFiee/QQON-Panel \
  --commit FULL_LOWERCASE_COMMIT_SHA \
  --build-time 2026-11-06T00:00:00Z \
  --output /absolute/existing-parent/new-output-directory
```

输出目录必须是全新绝对路径，其父目录要已存在；不会覆盖旧产物。不传 `--build-time` 时使用工具的当前 UTC 时间；想重复构建需固定同一时间。Windows 可用 `--go C:/path/to/go.exe` 指定工具链，工具始终从项目目录构建，不在下载目录运行解释器。

只验证已有发行目录、不执行其中的代码：

```sh
python3 -I tools/build_release.py --verify-only \
  --version v0.4.0 --repo LinyuFiee/QQON-Panel --commit FULL_LOWERCASE_COMMIT_SHA \
  --build-time 2026-11-06T00:00:00Z \
  --output /absolute/release-directory
```

`panel -build-info` 返回程序内部的公开构建身份。CI 在 Linux 构建任务中会执行自己的新 amd64 程序检查字段一致性；arm64 只验证静态 ELF，不假称已在 ARM 主机运行。

## 5. 发布失败与信任边界

- 测试或构建失败：不会创建可升级 Release。
- 上传失败：可能保留未发布草稿；人工检查后处理，不把半成品设为 Latest。已有同标签 Release 不会被静默覆盖；重跑前需先检查草稿与远端标签。
- 不要移动已发布标签、重新使用旧版本号或替换已经发布的程序文件。修复应发布新版本。
- SHA-256 校验检查完整性，但校验文件来自同一仓库，**不是独立发布者签名**。本版本信任该 GitHub 仓库和 HTTPS；仓库维护权限必须妥善保护。
- 官方 Actions 使用已核实的固定 commit；更新这些 pin 时需重新核实来源。
- 目前只支持公开仓库，不收用户 GitHub token，不使用任意镜像下载地址。

本地测试不等于 GitHub Actions 成功，更不等于 Linux 真实安装／升级成功。首次公开发布前应在测试仓库和测试 VPS 完整走一遍：安装 → 初始化 → 配置保存 → 发布新标签 → 更新 → 登录 → 校验数据 → 模拟启动失败回退。
