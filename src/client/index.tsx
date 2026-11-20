import type { Context } from '@deepseek-ai/cordis'
import type { MainPanelId } from '@deepseek-ai/dsh-client-ui-layout/client'
import type { SidebarPanelIconOwnerProps } from '@deepseek-ai/dsh-client-ui-sidebar/client'
import type {} from '@deepseek-ai/dsh-client-locale/client'
import type {} from '@deepseek-ai/dsh-client-ui-renderer/client'
import type {} from '@deepseek-ai/dsh-client-ui-layout/client'
import { App } from './app.js'
import { DiagnosticsPanel, PluginErrorBoundary, reportClientError, reportClientEvent } from './diagnostics.js'
import { en, zh, type T, type TextKey } from './locales.js'
import styles from './style.css?raw'

declare module '@deepseek-ai/dsh-client-ui-slots' {
  interface LocaleNamespaceMap { hgdbMonitor: TextKey }
}

const NS = 'hgdbMonitor'
const PANEL_ID = 'hgdb-monitor' as MainPanelId

interface WorkspaceNavigation {
  startSession(
    workspaceId?: unknown,
    options?: {
      prompt?: string
      whenDraftExists?: 'keep' | 'replace'
    },
  ): void
}

function MonitorIcon({ size }: SidebarPanelIconOwnerProps) {
  return <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
    <ellipse cx="12" cy="5" rx="7" ry="3" /><path d="M5 5v6c0 1.7 3.1 3 7 3s7-1.3 7-3V5" /><path d="M5 11v6c0 1.7 3.1 3 7 3" /><path d="m15 18 2 2 4-5" />
  </svg>
}

export const inject = ['slots', 'locale', 'layout', 'uiWorkspace']

export function apply(ctx: Context): void {
  try {
    ctx.effect(() => ctx.locale.register(NS, { zh, en }), 'hgdb_monitor: dictionaries')
    ctx.effect(() => {
      const style = document.createElement('style')
      style.dataset.hgdbMonitor = 'true'
      style.textContent = styles
      document.head.append(style)
      return () => style.remove()
    }, 'hgdb_monitor: styles')
    const t = ctx.locale.bind(NS) as T
    const workspaceNavigation = ctx.get('uiWorkspace') as unknown as WorkspaceNavigation
    const startAnalysis = async (instanceRef: string): Promise<void> => {
      const alias = `实例-${instanceRef.slice(-6)}`
      const prompt = `请分析数据库监控实例 ${alias}（instance_ref: ${instanceRef}）的当前状态。先调用 hgdb_monitor_diagnose_database；如需进一步定位，再按诊断事实调用会话、阻塞、Top SQL、复制或历史指标工具。不要给出没有规则和证据支持的健康评分。`
      // ===== FIXED 2026-11-19（AI 分析当前状态：新会话带不进提示文案）=====
      // 这里原先在 startSession(...) 之后紧跟着调用了 ctx.layout.selectPanel(null)，已删除。
      // 原因：layout.selectPanel 会 abort 当前的导航信号，而 startSession 的导航正停在
      // openWorkspaceSession 的 `await connectBlank(...)` 上；await 返回后它先检查
      // `navigation.aborted`，一旦为真就直接 return，beforeOpen（真正执行
      // conversation.input.requestDraftInitialization 写草稿的地方）因此永远不会被调用，
      // 表现就是「新对话框已打开，但输入框里没有这段文案」。
      // startSession 内部走的是 replaceMain(..., 'reveal')，写入草稿后会自己切回会话视图，
      // 所以不要再手动 selectPanel(null)；若确实需要先切换面板，必须放在 startSession 之前。
      // 注意：以下 prompt 是要带进新会话的固定文案，请勿在此处拼接实例指标数据。
      workspaceNavigation.startSession(undefined, { prompt, whenDraftExists: 'replace' })
    }
    ctx.slots.inject('main', () => ctx.slots.register(
      { name: 'main', key: PANEL_ID, locale: NS, inject: () => ({}) },
      (props: { t: T }) => <PluginErrorBoundary t={props.t}><DiagnosticsPanel><App t={props.t} onAnalyze={startAnalysis} /></DiagnosticsPanel></PluginErrorBoundary>,
    ))
    ctx.slots.inject('sidebar.panellist', () => ctx.slots.register(
      { name: 'sidebar.panellist', id: PANEL_ID, order: 20, label: () => t('panel'), locale: NS }, MonitorIcon,
    ))
    reportClientEvent('client_apply')
  } catch (error) {
    reportClientError('plugin_apply', error, 'client.apply')
    throw error
  }
}
