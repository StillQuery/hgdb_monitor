import { rm } from 'node:fs/promises'

await Promise.all([
  rm(new URL('../lib', import.meta.url), { recursive: true, force: true }),
  rm(new URL('../core/bin', import.meta.url), { recursive: true, force: true }),
])
