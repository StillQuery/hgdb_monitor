import { mkdir } from 'node:fs/promises'
import { spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'

const root = fileURLToPath(new URL('..', import.meta.url))
const output = fileURLToPath(new URL('../core/bin', import.meta.url))
await mkdir(output, { recursive: true })

const targets = [
  { goos: 'windows', goarch: 'amd64', file: 'hgdb-monitor-core-windows-amd64.exe' },
  { goos: 'linux', goarch: 'amd64', file: 'hgdb-monitor-core-linux-amd64' },
]

for (const target of targets) {
  await new Promise((resolve, reject) => {
    // -buildvcs=false：产物不应依赖本机的 git 状态。发布包、CI 或 .git 异常时
    // go build 取 VCS 信息会失败并中断整个打包流程，这里显式关闭版本信息注入。
    const child = spawn('go', [
      'build', '-trimpath', '-buildvcs=false', '-ldflags=-s -w',
      '-o', `${output}/${target.file}`,
      './core/cmd/hgdb-monitor-core',
    ], {
      cwd: root,
      env: { ...process.env, GOOS: target.goos, GOARCH: target.goarch, CGO_ENABLED: '0' },
      stdio: 'inherit',
      shell: false,
    })
    child.once('error', reject)
    child.once('exit', code => code === 0 ? resolve() : reject(new Error(`go build exited with ${code}`)))
  })
}
