import { defineConfig } from 'vitepress'

export default defineConfig({
  lang: 'zh-CN',
  title: 'QQON-Panel',
  description: 'QQON-Panel v0.7.0 预览版中文文档：安装、独立权限授权、Linux 软件与网站托管及验证边界。',
  base: '/QQON-Panel/',
  appearance: true,
  ignoreDeadLinks: false,
  lastUpdated: false,
  srcExclude: ['node_modules/**'],
  head: [
    ['link', { rel: 'icon', type: 'image/svg+xml', href: '/QQON-Panel/logo.svg' }],
    ['meta', { name: 'theme-color', content: '#2563eb' }]
  ],
  markdown: {
    theme: { light: 'github-light', dark: 'github-dark' },
    codeCopyButtonTitle: '复制代码'
  },
  themeConfig: {
    logo: { src: '/logo.svg', alt: 'QQON-Panel' },
    siteTitle: 'QQON-Panel',
    nav: [
      { text: '开始使用', link: '/guide/quickstart', activeMatch: '/guide/(quickstart|installation|legacy-v0-4)' },
      { text: '支持范围', link: '/guide/support-matrix' },
      { text: '运维指南', link: '/guide/security', activeMatch: '/guide/(security|windows-services|maintenance|public-https|hosting|upgrade-migration|troubleshooting)' },
      { text: '发布状态', link: '/guide/releases' },
      { text: 'GitHub', link: 'https://github.com/LinyuFiee/QQON-Panel' }
    ],
    sidebar: [
      {
        text: '开始使用',
        items: [
          { text: '快速开始', link: '/guide/quickstart' },
          { text: '支持矩阵与验证状态', link: '/guide/support-matrix' },
          { text: '预览版安装（v0.7.0）', link: '/guide/installation' },
          { text: '旧预览版 v0.4.0 安装', link: '/guide/legacy-v0-4' }
        ]
      },
      {
        text: '安全与维护',
        items: [
          { text: '安全边界', link: '/guide/security' },
          { text: 'Windows 服务', link: '/guide/windows-services' },
          { text: 'qqon 维护', link: '/guide/maintenance' },
          { text: '公网 IP HTTPS', link: '/guide/public-https' },
          { text: 'v0.7.0 托管指南', link: '/guide/hosting' },
          { text: '升级与迁移', link: '/guide/upgrade-migration' },
          { text: '故障排查', link: '/guide/troubleshooting' }
        ]
      },
      {
        text: '项目与发布',
        items: [
          { text: '发布状态与产物边界', link: '/guide/releases' },
          { text: '构建本文档', link: '/guide/docs-development' }
        ]
      }
    ],
    outline: { level: [2, 3], label: '本页目录' },
    sidebarMenuLabel: '菜单',
    skipToContentLabel: '跳转到内容',
    returnToTopLabel: '返回顶部',
    darkModeSwitchLabel: '外观',
    lightModeSwitchTitle: '切换到浅色模式',
    darkModeSwitchTitle: '切换到深色模式',
    docFooter: { prev: '上一篇', next: '下一篇' },
    notFound: {
      title: '页面未找到',
      quote: '链接可能已变更，请从首页或搜索继续查找。',
      linkLabel: '返回首页',
      linkText: '返回首页'
    },
    search: {
      provider: 'local',
      options: {
        disableQueryPersistence: true,
        miniSearch: {
          options: {
            // Keep this function self-contained: VitePress serializes it for the client.
            tokenize: (text) => {
              if (typeof Intl.Segmenter === 'function') {
                return Array.from(new Intl.Segmenter('zh-CN', { granularity: 'word' }).segment(text))
                  .filter((part) => part.isWordLike)
                  .map((part) => part.segment)
              }
              return text.match(/[\p{Script=Han}]|[\p{L}\p{N}_]+/gu) ?? []
            }
          }
        },
        locales: {
          root: {
            translations: {
              button: { buttonText: '搜索文档', buttonAriaLabel: '搜索文档' },
              modal: {
                displayDetails: '显示详细结果',
                resetButtonTitle: '清除搜索',
                backButtonTitle: '关闭搜索',
                noResultsText: '未找到相关结果',
                footer: { selectText: '选择', navigateText: '切换', closeText: '关闭' }
              }
            }
          }
        }
      }
    },
    footer: {
      message: '文档可以先于 Release 更新；请以发布状态与实际附件为准。',
      copyright: 'QQON-Panel · 本地搜索 · 无外部追踪脚本'
    }
  }
})
