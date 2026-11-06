import { lstat, mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const entries = [
  { source: new URL('../../scripts/bootstrap.sh', import.meta.url), output: new URL('../public/install.sh', import.meta.url) },
  { source: new URL('../../scripts/bootstrap.ps1', import.meta.url), output: new URL('../public/install.ps1', import.meta.url) }
]
const maxBytes = 512 * 1024

try {
  // Read and validate BOTH sources before publishing either generated entry.
  // Never execute a bootstrap, fetch a substitute, or emit placeholder code.
  const contents = await Promise.all(entries.map(async ({ source }) => {
    const info = await lstat(source)
    if (!info.isFile() || info.isSymbolicLink() || info.size === 0 || info.size > maxBytes) {
      throw new Error(`入口源必须为非空、最多 512 KiB 的普通文件：${fileURLToPath(source)}`)
    }
    const data = await readFile(source)
    if (data.length !== info.size) throw new Error(`入口源在读取时发生变化：${fileURLToPath(source)}`)
    return data
  }))
  await mkdir(new URL('../public/', import.meta.url), { recursive: true })
  for (const [index, entry] of entries.entries()) {
    // Generated destinations are owned by this build; do not follow a stale link.
    await rm(entry.output, { force: true })
    await writeFile(entry.output, contents[index], { flag: 'wx', mode: 0o644 })
  }
  console.log('已按字节复制两个真实 bootstrap 到 docs/public；未执行安装器。')
} catch (error) {
  await Promise.all(entries.map(({ output }) => rm(output, { force: true }).catch(() => {})))
  console.error('文档入口准备失败：两个 scripts/bootstrap.* 必须齐全；不会生成占位安装器。')
  console.error(error instanceof Error ? error.message : error)
  process.exitCode = 1
}
