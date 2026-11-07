---
layout: doc
sidebar: false
aside: false
prev: false
next: false
title: 一行命令开始
---

# QQON-Panel

轻量服务器面板。**[v0.5.0 已发布](https://github.com/LinyuFiee/QQON-Panel/releases/tag/v0.5.0)**，选择系统，复制一条命令后按提示操作。

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

随后在终端选择安装、升级或只读状态；首次安装可选端口和本地／已有 HTTPS 代理访问方式。

下一版 **v0.6 开发中，尚未发布**：[公网 IP HTTPS](./guide/public-https) · [qqon 中文维护](./guide/maintenance)；当前 v0.5 安装仍使用回环监听。

一行命令会执行本项目的远程引导脚本。[先下载审阅与安装要求](./guide/installation) · [快速开始](./guide/quickstart) · [支持与验证边界](./guide/support-matrix) · [安全边界](./guide/security) · [发布记录](./guide/releases) · [CI 结果](https://github.com/LinyuFiee/QQON-Panel/actions/runs/42328127375)
