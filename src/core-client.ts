import { createInterface } from 'node:readline'
import type { Context } from '@deepseek-ai/cordis'
import type { SubprocessHandle } from '@deepseek-ai/dsh-subprocess'
import type { HandshakeResult, RpcRequest, RpcResponse } from './protocol.js'

interface PendingCall {
  resolve(value: unknown): void
  reject(error: Error): void
  timer: NodeJS.Timeout
  removeAbort?: () => void
}

const PROTOCOL_VERSION = 1

export class CoreClient {
  readonly #ctx: Context
  readonly #executable: string
  readonly #cwd: string
  readonly #dataDir: string
  #handle: SubprocessHandle | undefined
  #pending = new Map<string, PendingCall>()
  #sequence = 0
  #disposed = false

  constructor(ctx: Context, executable: string, cwd: string, dataDir: string) {
    this.#ctx = ctx
    this.#executable = executable
    this.#cwd = cwd
    this.#dataDir = dataDir
  }

  async start(): Promise<void> {
    if (this.#disposed) throw new Error('hgdb_monitor core client is disposed')
    if (this.#handle !== undefined) return

    const handle = this.#ctx.subprocess.spawn({
      argv: [this.#executable, '--data-dir', this.#dataDir],
      cwd: this.#cwd,
      stdio: { stdin: 'pipe', stdout: 'pipe', stderr: 'inherit' },
      graceMs: 5_000,
    })
    if (handle.stdin === undefined || handle.stdout === undefined) {
      handle.terminate()
      throw new Error('hgdb_monitor core did not expose protocol pipes')
    }
    this.#handle = handle

    const lines = createInterface({ input: handle.stdout, crlfDelay: Infinity })
    lines.on('line', line => { this.#acceptLine(line) })
    void handle.done.then(
      outcome => this.#failAll(new Error(`hgdb_monitor core exited (${outcome.exitCode ?? outcome.signal ?? 'unknown'})`)),
      error => this.#failAll(error instanceof Error ? error : new Error(String(error))),
    )

    const handshake = await this.call<HandshakeResult>('system.handshake', { protocolVersion: PROTOCOL_VERSION })
    if (handshake.protocolVersion !== PROTOCOL_VERSION || handshake.service !== 'hgdb_monitor_core') {
      await this.dispose()
      throw new Error('hgdb_monitor core protocol mismatch')
    }
  }

  async call<T>(method: string, params?: unknown, timeoutMs = 10_000, signal?: AbortSignal): Promise<T> {
	const handle = this.#handle
	if (handle?.stdin === undefined) throw new Error('hgdb_monitor core is not running')
	if (signal?.aborted === true) throw signal.reason instanceof Error ? signal.reason : new DOMException('Operation aborted', 'AbortError')

    const id = `host-${++this.#sequence}`
    const request: RpcRequest = { id, method, ...(params === undefined ? {} : { params }) }
	return new Promise<T>((resolve, reject) => {
	  const timer = setTimeout(() => {
		const pending = this.#pending.get(id)
		if (pending === undefined) return
		this.#pending.delete(id)
		pending.removeAbort?.()
		this.#cancelRemote(id)
		reject(new Error(`hgdb_monitor core call timed out: ${method}`))
	  }, timeoutMs)
	  const onAbort = () => {
		const pending = this.#pending.get(id)
		if (pending === undefined) return
		clearTimeout(pending.timer)
		this.#pending.delete(id)
		pending.removeAbort?.()
		this.#cancelRemote(id)
		reject(signal?.reason instanceof Error ? signal.reason : new DOMException('Operation aborted', 'AbortError'))
	  }
	  if (signal !== undefined) signal.addEventListener('abort', onAbort, { once: true })
	  this.#pending.set(id, {
		resolve: value => resolve(value as T), reject, timer,
		...(signal === undefined ? {} : { removeAbort: () => signal.removeEventListener('abort', onAbort) }),
	  })
      handle.stdin!.write(`${JSON.stringify(request)}\n`, error => {
        if (error === null || error === undefined) return
        const pending = this.#pending.get(id)
        if (pending === undefined) return
		clearTimeout(pending.timer)
		this.#pending.delete(id)
		pending.removeAbort?.()
        pending.reject(error)
      })
    })
  }

  async dispose(): Promise<void> {
    if (this.#disposed) return
    this.#disposed = true
    const handle = this.#handle
    if (handle === undefined) return

    try {
      await this.callBeforeStop(handle)
    } finally {
      handle.stdin?.end()
      const controller = new AbortController()
      const timer = setTimeout(() => controller.abort(), 2_000)
      try {
        if (!await handle.waitForExit(controller.signal)) {
          handle.terminate()
          await handle.waitForExit()
        }
      } finally {
        clearTimeout(timer)
        this.#handle = undefined
        this.#failAll(new Error('hgdb_monitor core stopped'))
      }
    }
  }

  async callBeforeStop(handle: SubprocessHandle): Promise<void> {
    if (handle.stdin === undefined) return
    try {
      await this.call<void>('system.shutdown', undefined, 1_500)
    } catch (error) {
      this.#ctx.logger.warn(`hgdb_monitor: graceful core shutdown failed: ${String(error)}`)
    }
  }

  #acceptLine(line: string): void {
    let response: RpcResponse
    try {
      response = JSON.parse(line) as RpcResponse
    } catch {
      this.#ctx.logger.warn('hgdb_monitor: ignored malformed core response')
      return
    }
    const pending = this.#pending.get(response.id)
    if (pending === undefined) return
	clearTimeout(pending.timer)
	this.#pending.delete(response.id)
	pending.removeAbort?.()
    if (response.error !== undefined) pending.reject(new Error(`${response.error.code}: ${response.error.message}`))
    else pending.resolve(response.result)
  }

  #failAll(error: Error): void {
    for (const pending of this.#pending.values()) {
	  clearTimeout(pending.timer)
	  pending.removeAbort?.()
      pending.reject(error)
    }
	this.#pending.clear()
  }

  #cancelRemote(requestId: string): void {
	const stdin = this.#handle?.stdin
	if (stdin === undefined) return
	const request: RpcRequest = { id: `cancel-${++this.#sequence}`, method: 'system.cancel', params: { requestId } }
	stdin.write(`${JSON.stringify(request)}\n`, () => {})
  }
}
