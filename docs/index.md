---
layout: doc
sidebar: false
aside: false
prev: false
next: false
title: 一行命令开始
---

# QQON-Panel

轻量服务器面板。**[v0.7.0 预览版已发布](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.7.0)**，支持独立授权后的 Linux 软件安装、网站管理与后台任务，下方一键入口获取当前版本。

::: warning 版本与能力边界
预览版不等于稳定成熟或全平台实机验收。旧 v0.4.0／v0.5.0／v0.6.0 没有真实软件安装和建站；v0.7.0 的托管能力须独立同意 root／LocalSystem，普通 yes/update 不授权，Windows 软件／建站仍不支持。流程与[验证边界](./guide/hosting#verification)见托管指南。
:::

## Linux

在 **root 的交互式 Bash** 中执行，需已有 `curl`；[查看支持范围](./guide/support-matrix)。

```sh
curl -fsSL https://linyufiee.github.io/QQON-Panel/install.sh | bash
```

## Windows

在 **以管理员身份打开的 Windows PowerShell 5.1（x64）** 中执行。

```powershell
irm https://linyufiee.github.io/QQON-Panel/install.ps1 | iex
```

确认自己的公网 IPv4，默认使用自签 HTTPS `8443`；也可选择仅本机、已有 HTTPS 代理或导入证书。安装后运行 `qqon` 打开中文维护菜单。

自签证书会有浏览器警告，请核对可信终端显示的 SHA-256 指纹。公网链接只是配置结果，未检测外网可达性；云安全组、防火墙和 NAT 需人工配置。[公网 HTTPS 说明](./guide/public-https) · [维护命令](./guide/maintenance)

一行命令会执行本项目的远程引导脚本。[先下载审阅与安装要求](./guide/installation) · [快速开始](./guide/quickstart) · [支持与验证边界](./guide/support-matrix) · [安全边界](./guide/security) · [发布记录](./guide/releases) · [发布 CI](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42383705774)
