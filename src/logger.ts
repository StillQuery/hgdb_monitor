import { appendFile, mkdir, rename, rm, stat } from 'node:fs/promises'
import { join } from 'node:path'

const MAX_LOG_BYTES = 5 * 1024 * 1024
const ROTATED_FILES = 3
const MAX_TEXT_LENGTH = 4_000

export type LogLevel = 'info' | 'warn' | 'error'

export interface SafeLogFields {
  errorId?: string
  kind?: string
  component?: string
  operation?: string
  status?: number
  code?: string
  message?: string
  stack?: string
}

function redact(value: string): string {
  let text = value.slice(0, MAX_TEXT_LENGTH)
  text = text.replace(/\b(?:postgres(?:ql)?):\/\/[^\s]+/gi, '[REDACTED_CONNECTION]')
  text = text.replace(/\b(?:\d{1,3}\.){3}\d{1,3}\b/g, '[REDACTED_IP]')
  text = text.replace(/\b(password|passwd|pwd|token|secret)\s*[:=]\s*[^\s,;]+/gi, '$1=[REDACTED]')
  if (/\b(?:select|insert|update|delete|explain|with)\b[\s\S]*\b(?:from|into|set|values|select)\b/i.test(text)) {
    return '[REDACTED_SQL]'
  }
  return text
}

function safeFields(fields: SafeLogFields): Record<string, string | number> {
  const result: Record<string, string | number> = {}
  for (const key of ['errorId', 'kind', 'component', 'operation', 'code'] as const) {
    const value = fields[key]
    if (value !== undefined) result[key] = redact(value)
  }
  if (fields.status !== undefined) result.status = fields.status
  if (fields.message !== undefined) result.message = redact(fields.message)
  if (fields.stack !== undefined) result.stack = redact(fields.stack)
  return result
}

export class PluginLogger {
  readonly path: string
  readonly #directory: string
  #queue: Promise<void> = Promise.resolve()

  constructor(directory: string) {
    this.#directory = directory
    this.path = join(directory, 'plugin.log')
  }

  info(event: string, fields: SafeLogFields = {}): void { this.#write('info', event, fields) }
  warn(event: string, fields: SafeLogFields = {}): void { this.#write('warn', event, fields) }
  error(event: string, fields: SafeLogFields = {}): void { this.#write('error', event, fields) }

  async flush(): Promise<void> { await this.#queue }

  #write(level: LogLevel, event: string, fields: SafeLogFields): void {
    const line = `${JSON.stringify({ time: new Date().toISOString(), level, event: redact(event), ...safeFields(fields) })}\n`
    this.#queue = this.#queue.then(async () => {
      await mkdir(this.#directory, { recursive: true })
      await this.#rotateIfNeeded(Buffer.byteLength(line))
      await appendFile(this.path, line, { encoding: 'utf8', mode: 0o600 })
    }).catch(() => undefined)
  }

  async #rotateIfNeeded(incomingBytes: number): Promise<void> {
    const size = await stat(this.path).then(value => value.size, () => 0)
    if (size + incomingBytes <= MAX_LOG_BYTES) return
    for (let index = ROTATED_FILES; index >= 1; index -= 1) {
      const source = index === 1 ? this.path : `${this.path}.${index - 1}`
      const destination = `${this.path}.${index}`
      await rm(destination, { force: true }).catch(() => undefined)
      await rename(source, destination).catch(() => undefined)
    }
  }
}
