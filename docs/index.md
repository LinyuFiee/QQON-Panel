---
layout: doc
sidebar: false
aside: false
prev: false
next: false
title: 一行命令开始
---

# QQON-Panel

轻量服务器面板。选择系统，审阅安全边界后开始安装。

::: warning v0.5 待发布 · 当前可下载版本为 v0.4.0
以下交互入口需要 **v0.5.0 或更高版本的正式 Release**。当前只有 v0.4.0，入口会拒绝不兼容版本，不会安装成功；**Windows 安装包尚未发布**。现在要安装，请走 [v0.4.0 Linux 安装指南](./guide/legacy-v0-4)。
:::

## Linux

在 **root 的交互式 Bash** 中执行，需已有 `curl`；[查看支持范围](./guide/support-matrix)。

```sh
curl -fsSL https://linyufiee.github.io/QQON-Panel/install.sh | bash
```

## Windows

在 **以管理员身份打开的 Windows PowerShell 5.1（x64）** 中执行。此路径待发布，不代表已实机支持所有 Windows 版本。

```powershell
irm https://linyufiee.github.io/QQON-Panel/install.ps1 | iex
```

一行命令会立即执行远程脚本，信任 GitHub Pages、本项目仓库及其 Release。[先下载审阅与安装要求](./guide/installation) · [快速开始](./guide/quickstart) · [安全边界](./guide/security) · [发布状态](./guide/releases)
