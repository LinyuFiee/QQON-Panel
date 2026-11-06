# 构建本文档

本文档使用 **VitePress 1.6.4 稳定版**与默认中文化主题，依赖和 lockfile 均在 `docs` 下。没有外部搜索 API、CDN 字体或追踪脚本；本地搜索索引和静态资源随站点发布。

## 本地构建

维护者使用 Node.js 24 LTS 与 npm；`package.json` 允许 Node.js 22 及以上。以下命令从仓库根目录执行，只安装文档构建依赖，不安装面板到本机，也不操作服务：

```sh
npm --prefix docs ci
npm --prefix docs run build
npm --prefix docs audit
```

`npm ci` 使用已提交的 lockfile；仅在有意调整依赖时使用 `npm install` 更新它。依赖审计与实际线上静态页面是否受影响是两件事；有 advisory 时记录完整结果，不用强制大版本升级隐藏问题。

::: warning 当前依赖审计结果
2026-11-07 本次 `npm audit` 报告 **3 项：2 moderate、1 high**，涉及 VitePress 1.6.4 → Vite 5.4.21 → esbuild 0.21.5，npm 未给出该稳定依赖树的可用修复。报告涉及开发服务器、文件访问及 Windows 编辑器接口；静态 Pages 产物不会运行这些接口，但不能因此把依赖审计标为通过。当前未使用强制 override 或 2.0 alpha 来隐藏问题；不要把开发/预览服务开放给不可信网络。**仅把开发服务器绑定回环也不能消除所有跨站访问问题**，核对静态产物应使用与 Vite 开发接口分离的本地静态服务器。
:::

<!-- 维护者审计记录（2026-11-07）：npm registry latest=1.6.4，next=2.0.0-alpha.21。
完整 npm audit：2 moderate + 1 high，fixAvailable=false；--omit=dev 为 0 不代表完整审计通过。
高危 Windows launch-editor file 参数/命令注入适用条件：
https://github.com/advisories/GHSA-587h-7wmj-h5pm
esbuild serve CORS 公告含攻击 127.0.0.1 的例子，不能声称 loopback dev server 自动安全：
https://github.com/advisories/GHSA-67mh-4wv8-2f99
本次仅静态解析/构建验证，不开启 Vite dev/editor endpoints；不记录本机私有路径或凭据。
-->

源码/Release 构建另需 **Go 1.26+**，但构建本文档不需要 Go；用户服务器运行已发布程序也不需要 Node/npm。

## 构建前安装入口

`prebuild` 读取仓库 `scripts/bootstrap.sh`、`scripts/bootstrap.ps1`，确认两者都是存在、非空且在限额内的普通文件，再按字节复制到：

- `docs/public/install.sh`
- `docs/public/install.ps1`

任何源文件缺失都会失败；没有占位脚本、下载回退或假成功。这两个副本是生成文件，不手工维护，不提交到版本控制；产物会复制到 `docs/.vitepress/dist`。构建脚本本身不执行安装器。

## 链接检查

VitePress 构建时保留死链接检查；`postbuild` 额外检查生成 HTML 中的站内链接、锚点、本地资源、`/QQON-Panel/` 项目 base 与入口副本的字节一致性。不启动浏览器、不访问外部链接，也不能代替真实部署或目标机安装测试。

已有构建产物时可单独检查：

```sh
npm --prefix docs run check:links
```

## 独立 Pages workflow

`.github/workflows/docs.yml` 与程序 Release workflow 分离：

- 只处理 `main` 上的文档、两个 bootstrap 和本 workflow 变更，也支持手动触发。
- 构建使用 `npm ci`、入口复制、VitePress 构建与本地链接检查；只上传静态站点目录。
- 构建任务仅有读取源码权限，部署任务才有 Pages 与 OIDC 写权限；不授予仓库内容写权限。
- 使用固定完整提交 SHA 的官方 actions，checkout 不保留凭据。
- Pages 部署不创建 Release/tag，不改旧附件，也不安装或启动面板服务。

仓库维护者需在 GitHub Pages 设置中选择 **GitHub Actions**，并核对 Pages 环境、域名和权限。本地构建通过只证明静态站点生成成功；实际发布与浏览器/移动端验收需要另外完成。
