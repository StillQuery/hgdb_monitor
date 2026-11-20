import type { IncomingMessage, ServerResponse } from 'node:http'
import type { Context } from '@deepseek-ai/cordis'
import type {} from '@deepseek-ai/dsh-host-webserver'
import type { CoreClient } from './core-client.js'
import type { PluginLogger } from './logger.js'

const PREFIX = '/hgdb-monitor/api/'
const REQUEST_HEADER = 'x-hgdb-monitor-request'
const MAX_BODY_BYTES = 64 * 1024

class HttpError extends Error {
  constructor(readonly status: number, message: string) { super(message) }
}

async function body(req: IncomingMessage): Promise<Record<string, unknown>> {
  let size = 0
  const chunks: Buffer[] = []
  for await (const chunk of req) {
    const bytes = Buffer.from(chunk)
    size += bytes.length
    if (size > MAX_BODY_BYTES) throw new HttpError(413, 'request body is too large')
    chunks.push(bytes)
  }
  try {
    const value = JSON.parse(Buffer.concat(chunks).toString('utf8')) as unknown
    if (value === null || typeof value !== 'object' || Array.isArray(value)) throw new Error('not an object')
    return value as Record<string, unknown>
  } catch {
    throw new HttpError(400, 'request body must be a JSON object')
  }
}

function textField(value: unknown, maximum: number): string | undefined {
  return typeof value === 'string' && value.length > 0 ? value.slice(0, maximum) : undefined
}

async function clientError(req: IncomingMessage, logger: PluginLogger): Promise<{ accepted: true }> {
  const value = await body(req)
  const errorId = textField(value.errorId, 80)
  const kind = textField(value.kind, 80)
  if (errorId === undefined || !/^[a-z0-9-]{1,80}$/i.test(errorId) || kind === undefined) {
    throw new HttpError(400, 'invalid client error report')
  }
  logger.error('client_error', {
    errorId,
    kind,
    ...(textField(value.component, 120) === undefined ? {} : { component: textField(value.component, 120)! }),
    ...(textField(value.message, 1_000) === undefined ? {} : { message: textField(value.message, 1_000)! }),
    ...(textField(value.stack, 8_000) === undefined ? {} : { stack: textField(value.stack, 8_000)! }),
  })
  return { accepted: true }
}

async function clientEvent(req: IncomingMessage, logger: PluginLogger): Promise<{ accepted: true }> {
  const value = await body(req)
  const event = textField(value.event, 80)
  if (event !== 'client_apply' && event !== 'panel_mounted') throw new HttpError(400, 'invalid client event')
  logger.info(event)
  return { accepted: true }
}

function json(res: ServerResponse, status: number, value: unknown): void {
  res.writeHead(status, {
    'content-type': 'application/json; charset=utf-8',
    'cache-control': 'no-store',
    'x-content-type-options': 'nosniff',
  })
  res.end(JSON.stringify(value))
}

function allowed(req: IncomingMessage, ctx: Context): boolean {
  const port = ctx.webServer.port
  if (port === undefined) return false
  const host = req.headers.host
  const expected = `${ctx.webServer.host}:${port}`
  if (host !== expected && !(ctx.webServer.host === '127.0.0.1' && host === `localhost:${port}`)) return false
  return req.headers[REQUEST_HEADER] === '1' &&
    (req.headers.origin === undefined || req.headers.origin === `${ctx.webServer.protocol}//${host}`)
}

function idSegment(value: string | undefined): string {
  if (value === undefined || !/^[a-z0-9_-]{1,80}$/i.test(value)) throw new HttpError(400, 'invalid datasource id')
  return value
}

function rpcFailure(error: unknown): [number, { error: string; code?: string }] {
  const message = error instanceof Error ? error.message : 'operation failed'
  const match = /^([A-Z][A-Z0-9_]+):\s*(.*)$/.exec(message)
  if (match === null) return [500, { error: 'operation failed' }]
  const code = match[1]!
  const publicMessage = match[2] || 'operation failed'
  const status = code === 'DATASOURCE_NOT_FOUND' ? 404 : code === 'DATASOURCE_LIMIT_REACHED' ? 409 : 400
  return [status, { error: publicMessage, code }]
}

function operation(path: string, method: string): string {
  const segments = path.split('/').filter(Boolean)
  if (segments[0] === 'client-errors') return 'client-errors'
  if (segments[0] === 'client-events') return 'client-events'
  if (segments[0] === 'storage') return 'get-storage'
  if (segments[0] !== 'datasources') return 'unknown'
  if (segments.length === 1) return `${method.toLowerCase()}-datasources`
  return segments[2] === undefined ? `${method.toLowerCase()}-datasource` : `${method.toLowerCase()}-datasource-${segments[2]}`
}

async function route(req: IncomingMessage, client: CoreClient, logger: PluginLogger, path: string, signal: AbortSignal): Promise<unknown> {
  const method = req.method ?? 'GET'
  const url = new URL(req.url ?? '/', 'http://localhost')
  const segments = path.split('/').filter(Boolean)
  if (method === 'POST' && segments.length === 1 && segments[0] === 'client-errors') {
    return clientError(req, logger)
  }
  if (method === 'POST' && segments.length === 1 && segments[0] === 'client-events') {
    return clientEvent(req, logger)
  }
  if (method === 'GET' && segments.length === 1 && segments[0] === 'datasources') {
    return client.call('datasource.list', undefined, 10_000, signal)
  }
  if (method === 'GET' && segments.length === 1 && segments[0] === 'storage') {
    return client.call('storage.status', undefined, 10_000, signal)
  }
  if (method === 'POST' && segments.length === 1 && segments[0] === 'datasources') {
    return client.call('datasource.create', await body(req), 15_000, signal)
  }
  if (segments[0] !== 'datasources') throw new HttpError(404, 'unknown operation')
  const id = idSegment(segments[1])
  if (method === 'PUT' && segments.length === 2) {
    return client.call('datasource.update', { ...(await body(req)), id }, 15_000, signal)
  }
  if (method === 'DELETE' && segments.length === 2) {
    return client.call('datasource.delete', { id }, 15_000, signal)
  }
  if (method === 'POST' && segments[2] === 'test' && segments.length === 3) {
    return client.call('datasource.test', { id }, 15_000, signal)
  }
  if (method === 'PUT' && segments[2] === 'persistent' && segments.length === 3) {
    const value = await body(req)
    if (typeof value.enabled !== 'boolean') throw new HttpError(400, 'enabled must be boolean')
    return client.call('datasource.setPersistent', { id, enabled: value.enabled }, 15_000, signal)
  }
  if (method === 'GET' && segments[2] === 'latest' && segments.length === 3) {
    return client.call('monitor.latest', { id }, 10_000, signal)
  }
  if (method === 'GET' && segments[2] === 'current' && segments.length === 3) {
    return client.call('monitor.current', { id }, 15_000, signal)
  }
  if (method === 'GET' && segments[2] === 'activities' && segments.length === 3) {
    const kind = url.searchParams.get('kind') ?? 'active'
    const limit = Math.min(20, Math.max(1, Number.parseInt(url.searchParams.get('limit') ?? '8', 10) || 8))
    return client.call('monitor.activities', { id, kind, limit }, 15_000, signal)
  }
  if (method === 'GET' && segments[2] === 'lock-waits' && segments.length === 3) {
    const limit = Math.min(20, Math.max(1, Number.parseInt(url.searchParams.get('limit') ?? '8', 10) || 8))
    return client.call('monitor.lockWaits', { id, limit }, 15_000, signal)
  }
  if (method === 'GET' && segments[2] === 'top-sql' && segments.length === 3) {
    const order = url.searchParams.get('order') ?? 'total_time'
    const limit = Math.min(20, Math.max(1, Number.parseInt(url.searchParams.get('limit') ?? '10', 10) || 10))
    return client.call('monitor.topSQL', { id, order, limit }, 15_000, signal)
  }
  if (method === 'GET' && segments[2] === 'replication' && segments.length === 3) {
    const includeSlots = url.searchParams.get('includeSlots') !== 'false'
    return client.call('monitor.replication', { id, includeSlots }, 15_000, signal)
  }
  if (method === 'GET' && segments[2] === 'history' && segments.length === 3) {
    const minutes = Math.min(120, Math.max(1, Number.parseInt(url.searchParams.get('minutes') ?? '120', 10) || 120))
    const to = new Date()
    const from = new Date(to.getTime() - minutes * 60_000)
    return client.call('monitor.history', { id, from: from.toISOString(), to: to.toISOString() }, 10_000, signal)
  }
  throw new HttpError(404, 'unknown operation')
}

export function registerManagementApi(ctx: Context, client: CoreClient, logger: PluginLogger): () => void {
  // 已知限制：host 非 loopback 时整个插件拒绝加载（含 AI 工具与采集），见 docs/known-limitations.md
  if (ctx.webServer.host !== '127.0.0.1') throw new Error('hgdb_monitor management API requires a loopback Harness web server')
  return ctx.webServer.register({
    kind: 'prefix', path: '/hgdb-monitor', handler: async (req, res) => {
      const url = new URL(req.url ?? '/', 'http://localhost')
      if (!url.pathname.startsWith(PREFIX)) { json(res, 404, { error: 'not found' }); return }
      if (!allowed(req, ctx)) { json(res, 403, { error: 'same-origin management request required' }); return }
      const controller = new AbortController()
      req.once('aborted', () => controller.abort())
      const path = url.pathname.slice(PREFIX.length)
      const operationName = operation(path, req.method ?? 'GET')
      try {
        json(res, 200, await route(req, client, logger, path, controller.signal))
      } catch (error) {
        if (controller.signal.aborted) return
        if (error instanceof HttpError) {
          logger.warn('api_error', { operation: operationName, status: error.status, message: error.message })
          json(res, error.status, { error: error.message })
        } else {
          const [status, value] = rpcFailure(error)
          logger.error('api_error', { operation: operationName, status, ...(value.code === undefined ? {} : { code: value.code }), message: value.error })
          json(res, status, value)
        }
      }
    },
  })
}
