import { readdir, readFile } from 'node:fs/promises'
import { extname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const base = '/QQON-Panel/'
const origin = 'https://linyufiee.github.io'
const root = fileURLToPath(new URL('./dist/', import.meta.url))
const failures = []
const files = new Set()
const pages = new Map()

function decode(value) {
  return value.replace(/&(amp|quot|apos|lt|gt|#x[\da-f]+|#\d+);/gi, (entity, name) => {
    const names = { amp: '&', quot: '"', apos: "'", lt: '<', gt: '>' }
    const key = name.toLowerCase()
    if (key in names) return names[key]
    const point = key.startsWith('#x') ? parseInt(key.slice(2), 16) : Number(key.slice(1))
    return point > 0 && point <= 0x10ffff ? String.fromCodePoint(point) : entity
  })
}

async function collect(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const full = join(directory, entry.name)
    if (entry.isDirectory()) await collect(full)
    else if (entry.isFile()) {
      const name = relative(root, full).replaceAll('\\', '/')
      files.add(name)
      if (name.endsWith('.html')) {
        const html = await readFile(full, 'utf8')
        const ids = new Set([...html.matchAll(/\bid=(?:"([^"]*)"|'([^']*)')/g)].map((match) => decode(match[1] ?? match[2])))
        pages.set(name, { html, ids })
      }
    } else failures.push(`构建产物不是普通文件/目录：${full}`)
  }
}

function destination(pathname) {
  const name = decodeURIComponent(pathname.slice(base.length))
  const candidates = name.endsWith('/') || name === ''
    ? [`${name}index.html`]
    : extname(name) ? [name] : [name, `${name}.html`, `${name}/index.html`]
  return candidates.find((candidate) => files.has(candidate))
}

try {
  await collect(root)
  if (!pages.has('index.html')) failures.push('缺少首页 index.html；请先完成文档构建。')
  let checked = 0
  for (const [name, { html }] of pages) {
    const pageURL = new URL(`${base}${name}`, origin)
    for (const tag of html.matchAll(/<(a|link|script|img|source)\b[^>]*>/gi)) {
      const attribute = /\b(?:href|src)=(?:"([^"]*)"|'([^']*)')/i.exec(tag[0])
      if (!attribute) continue
      const value = decode(attribute[1] ?? attribute[2]).trim()
      if (!value || /^(?:mailto:|tel:)/i.test(value)) continue
      if (/^(?:data:|blob:)/i.test(value) && /^(?:img|source)$/i.test(tag[1])) continue
      let url
      try { url = new URL(value, pageURL) } catch {
        failures.push(`${name}：无法解析链接 ${value}`)
        continue
      }
      if (!['https:', 'http:'].includes(url.protocol)) {
        failures.push(`${name}：不允许的链接协议 ${value}`)
        continue
      }
      if (url.origin !== origin) {
        // External navigation is intentional; external runtime assets are not.
        if (tag[1].toLowerCase() !== 'a') failures.push(`${name}：外部运行资源 ${value}`)
        continue
      }
      if (!url.pathname.startsWith(base)) {
        failures.push(`${name}：站内链接缺少项目 base ${value}`)
        continue
      }
      checked += 1
      let target
      try { target = destination(url.pathname) } catch {
        failures.push(`${name}：无效路径编码 ${value}`)
        continue
      }
      if (!target) {
        failures.push(`${name}：站内目标不存在 ${value}`)
        continue
      }
      if (url.hash && pages.has(target)) {
        let id
        try { id = decodeURIComponent(url.hash.slice(1)) } catch {
          failures.push(`${name}：无效锚点编码 ${value}`)
          continue
        }
        if (id && !pages.get(target).ids.has(id)) failures.push(`${name}：锚点不存在 ${value}`)
      }
    }
  }

  for (const [sourceName, outputName] of [['bootstrap.sh', 'install.sh'], ['bootstrap.ps1', 'install.ps1']]) {
    const source = await readFile(new URL(`../../scripts/${sourceName}`, import.meta.url))
    const publicCopy = await readFile(new URL(`../public/${outputName}`, import.meta.url))
    const builtCopy = await readFile(join(root, outputName))
    if (!source.equals(publicCopy) || !source.equals(builtCopy)) failures.push(`${outputName} 与真实 bootstrap 源文件不一致`)
  }

  if (failures.length) throw new Error([...new Set(failures)].join('\n'))
  console.log(`已检查 ${pages.size} 个 HTML 页面、${checked} 个站内链接/资源及锚点；base 与两个安装入口副本一致。`)
  console.log('只检查本地静态产物；未执行安装器、未访问外部链接、未启动浏览器或服务。')
} catch (error) {
  console.error('文档链接/产物检查失败。')
  console.error(error instanceof Error ? error.message : error)
  process.exitCode = 1
}
