import { Component, useEffect, type ErrorInfo, type ReactNode } from 'react'
import type { T } from './locales.js'

const HEADER = { 'content-type': 'application/json', 'x-hgdb-monitor-request': '1' }
const recent = new Map<string, number>()

function errorId(): string {
  return typeof crypto.randomUUID === 'function'
    ? crypto.randomUUID()
    : `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`
}

function value(error: unknown): { message: string; stack?: string } {
  if (error instanceof Error) return { message: error.message, ...(error.stack === undefined ? {} : { stack: error.stack }) }
  return { message: String(error) }
}

export function reportClientError(kind: string, error: unknown, component?: string, componentStack?: string): string {
  const id = errorId()
  const detail = value(error)
  console.error(`[hgdb_monitor:${id}] ${kind}`, error)
  const fingerprint = `${kind}:${detail.message}`
  const now = Date.now()
  if (now - (recent.get(fingerprint) ?? 0) < 60_000) return id
  recent.set(fingerprint, now)
  const stack = [detail.stack, componentStack].filter(Boolean).join('\n').slice(0, 8_000)
  void fetch('/hgdb-monitor/api/client-errors', {
    method: 'POST',
    headers: HEADER,
    body: JSON.stringify({
      errorId: id,
      kind,
      ...(component === undefined ? {} : { component }),
      message: detail.message.slice(0, 1_000),
      ...(stack.length === 0 ? {} : { stack }),
    }),
  }).catch(() => undefined)
  return id
}

export function reportClientEvent(event: 'client_apply' | 'panel_mounted'): void {
  void fetch('/hgdb-monitor/api/client-events', {
    method: 'POST', headers: HEADER, body: JSON.stringify({ event }),
  }).catch(() => undefined)
}

export function DiagnosticsPanel({ children }: { children: ReactNode }) {
  useEffect(() => { reportClientEvent('panel_mounted') }, [])
  return children
}

interface BoundaryState { errorId?: string }

export class PluginErrorBoundary extends Component<{ children: ReactNode; t: T }, BoundaryState> {
  override state: BoundaryState = {}

  static getDerivedStateFromError(): BoundaryState { return { errorId: 'pending' } }

  override componentDidCatch(error: Error, info: ErrorInfo): void {
    this.setState({ errorId: reportClientError('react_render', error, 'App', info.componentStack ?? undefined) })
  }

  override render(): ReactNode {
    if (this.state.errorId !== undefined) {
      return <main className="hgm-page hgm-fatal">
        <section>
          <h1>{this.props.t('panelCrashed')}</h1>
          <p>{this.props.t('errorId')} <code>{this.state.errorId}</code></p>
          <p>{this.props.t('checkLog')} <code>$DSH_HOME/logs/hgdb_monitor/plugin.log</code></p>
        </section>
      </main>
    }
    return this.props.children
  }
}
