# 安装与升级脚本

这两个脚本只用于 **Debian 12/13、Ubuntu 22.04/24.04、amd64/arm64、systemd**。安装需要 root；安装后的面板以独立低权限账号运行。Windows 开发环境不支持真实安装。

先从 [QQON-Panel Release](https://github.com/LinyuFiee/QQON-Panel/releases/latest) 下载安装器并审阅，再执行：

```sh
sudo bash install.sh --repo LinyuFiee/QQON-Panel
sudo bash install.sh --repo LinyuFiee/QQON-Panel --version v0.4.0 --origin https://panel.example.com
```

脚本先校验 root、发行版和已运行的 systemd，再检查所需依赖。缺少 Python 3、CA 证书或必要的账号／系统工具时，会列出最小包集合，确认后通过现有 APT 源自动安装；显式 `--yes` 可无人值守。不执行整机升级，不改软件源，不安装 Go、Node、数据库或 Docker。依赖失败时不会停止旧面板。默认回环监听，不自动设置域名、TLS 证书、代理或防火墙。

安装后：

```sh
sudo panel-update
sudo panel-update --version v0.4.1
```

使用 `bash install.sh --help` 查看参数。无人值守只有显式选择 `--yes` 才跳过操作确认。

## 固定范围

- 服务账号／组：`server-panel`
- systemd unit：`server-panel.service`
- 面板程序：`/usr/local/bin/panel`
- 本地升级入口：`/usr/local/bin/panel-update`
- 已审阅安装器副本：`/usr/local/lib/server-panel/install.sh`
- root 所有的安装记录：`/etc/server-panel/install.json`
- 面板私有数据：`/var/lib/server-panel`

程序、升级器、unit 和安装记录必须由 root 持有，不能让面板用户修改。发现未知安装、符号链接、危险权限或记录与文件不匹配会拒绝继续，不自动“接管”旧文件。用户自己以前手动复制的二进制不是脚本管理安装，需先规划迁移，不强制覆盖。

## 升级过程

1. 获取独立更新文件锁，读取已安装记录。
2. 只从固定仓库查询稳定 Release，按架构下载；HTTPS 重定向仅允许指定 GitHub 资源主机。
3. 检查下载限额、压缩包 SHA-256、format2 版本清单，再受限展开唯一普通 `panel` 文件并校验 ELF 大小／摘要／架构／内嵌身份；同版本不重复安装，拒绝降级和不同数据格式。
4. 下载／校验期间旧服务保持运行。
5. 保留旧程序，在目标文件系统暂存；停止旧服务后原子切换。
6. 启动新服务，向本机健康接口发送正确 Host 并核对运行版本。
7. 新版启动或健康检查失败时尝试还原旧程序；回退失败也明确报告，不伪装成功。

升级在 `/etc/server-panel/update-transaction.json` 保存 root:root、0600 的事务记录：先写 `prepared`，旧程序备份落盘后写 `ready`，再停服切换。若遇断电或 SIGKILL，下一次 `sudo panel-update` 会在持锁后先校验事务、旧程序摘要与构建身份，尝试恢复旧版并检查健康；成功后清理事务并结束本次调用，**不会自动重试失败的新版本**。恢复失败则报告原因并保留可重试记录；不要手动修改记录或删除备份。持续磁盘写入故障可能妨碍恢复和记录保存，不承诺绝对恢复。

更新不会清理账户、TOTP、源配置或数据锁文件；内存会话会因重启失效。新二进制只能在完整校验后检查 `-build-info`，且不从下载目录运行 Python。Release 中的新脚本不会在更新时被下载执行，也不会自动覆盖本机已审阅的更新器。

## 压缩包与旧开发安装

0.4.0 起使用发行清单 format2，两个架构分别为 `panel-linux-amd64.tar.gz`、`panel-linux-arm64.tar.gz`，包内只有顶层普通 `panel`。限制压缩和展开体积，拒绝路径、软硬链接、特殊文件、扩展头、多文件及损坏 gzip；不调用无约束 `extractall`。解压后的程序摘要仍是回退和安装记录中的摘要。

旧开发版裸二进制的 format1 更新器不能识别新压缩格式，不能仅运行旧 `panel-update` 期待自动迁移。手动复制的旧程序或旧脚本管理安装需要管理员先备份私有数据，在独立路径测试后按新安装流程迁移；本版拒绝不受管理的安装，不提供强制覆盖或自动重置。新的 Release 不会偷偷替换管理员已审阅的本地升级器。

## 重要限制

- 校验清单和程序同属配置仓库，SHA-256 不提供独立发布者身份签名。
- schema 1 才支持此普通升级／回退流程；不对任意数据迁移作安全回退承诺。
- 可捕获失败／中断会执行受控恢复；断电、SIGKILL、磁盘或系统损坏后的恢复能力仍应在目标 Linux 上测试，不能把临时目录测试当成灾难恢复保证。
- 首次初始化没有默认密码；按服务启动输出路径读取本机引导令牌。

## 本地测试，不操作主机

```sh
bash -n scripts/install.sh
bash -n scripts/update.sh
python3 -I -m unittest discover -s scripts/tests -v
```

测试将核心逻辑加载到隔离环境，用临时目录、模拟下载、模拟账号和 systemctl，覆盖成功、拒绝、恢复路径；不会调用真实 root 安装。实际 GitHub、Linux 文件所有权、systemd 和版本重启需要测试机再验证。
