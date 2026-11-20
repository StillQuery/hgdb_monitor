import type { Context } from '@deepseek-ai/cordis'
import { defineTool } from '@deepseek-ai/dsh-tools'
import type { CoreClient } from './core-client.js'

const forbiddenKeys = /^(host|hostname|port|database|databaseName|user|username|clientAddress|applicationName|password|token|secret|connectionString)$/i
const forbiddenText = /(?:\b(?:postgres(?:ql)?):\/\/|\b(?:https?|ftp):\/\/|\b(?:\d{1,3}\.){3}\d{1,3}\b|\b(?:password|passwd|pwd|token|secret)\s*[:=])/i

function assertSafe(value: unknown, path = '$'): void {
  if (value === null || value === undefined || typeof value === 'number' || typeof value === 'boolean') return
  if (typeof value === 'string') {
    if (forbiddenText.test(value)) throw new Error(`SECURITY_REDACTION_FAILED: unsafe AI output at ${path}`)
    return
  }
  if (Array.isArray(value)) { value.forEach((item, index) => assertSafe(item, `${path}[${index}]`)); return }
  if (typeof value === 'object') {
    for (const [key, child] of Object.entries(value)) {
      if (forbiddenKeys.test(key)) throw new Error(`SECURITY_REDACTION_FAILED: forbidden AI field at ${path}.${key}`)
      assertSafe(child, `${path}.${key}`)
    }
  }
}

const jsonOutput = {
  schema: { type: 'json' as const },
  render: (_args: unknown, value: unknown) => { assertSafe(value); return [{ type: 'text' as const, text: JSON.stringify(value) }] },
}

async function safeCall<T>(client: CoreClient, method: string, params: unknown, timeoutMs: number, signal: AbortSignal): Promise<T> {
  const value = await client.call<T>(method, params, timeoutMs, signal)
  assertSafe(value)
  return value
}

export function registerMonitorTools(ctx: Context, client: CoreClient): () => void {
  const disposers = [
    ctx.tools.register(defineTool({
      name: 'hgdb_monitor_list_instances',
      description: 'Use when the user names an instance, has not identified one, asks “有哪些数据库/实例”, or wants to compare instances. Match the user’s wording against instanceName, then pass the matching instance_ref to other tools. Returns anonymous names only: no host, port, database, user, credential, or connection string; a read-write instance does not imply that a standby exists.',
      parameters: {}, output: jsonOutput, timeoutMs: 5_000, isConcurrencySafe: () => true,
      execute: async (_args, exec) => safeCall(client, 'ai.instances', undefined, 5_000, exec.signal),
    })),
    ctx.tools.register(defineTool({
      name: 'hgdb_monitor_diagnose_database',
      description: 'Use when the user asks whether the database is healthy, why it is slow, whether connections are sufficient, whether there is blocking, or how replication is doing (“数据库正常吗”, “为什么变慢了”, “连接够不够”, “有没有阻塞”, “复制怎么样”). Combines current facts with an optional comparison against the preceding equal window and returns rule-backed observations, not a health score.',
      parameters: {
        instance_ref: { type: 'string', required: true, description: 'Anonymous instance reference returned by hgdb_monitor_list_instances.' },
        focus: { type: 'string', enum: ['overall', 'performance', 'connections', 'blocking', 'replication'], description: 'Question area; defaults to overall.' },
        window_minutes: { type: 'integer', description: 'Recent window in minutes; 5–120, but at most 60 when compare_previous is true; defaults to 30.' },
        compare_previous: { type: 'boolean', description: 'Compare with the immediately preceding equal window; defaults to true and requires persistent collection.' },
      }, output: jsonOutput, timeoutMs: 15_000, isConcurrencySafe: () => true,
      execute: async (args, exec) => safeCall(client, 'ai.diagnose', { instanceRef: args.instance_ref, focus: args.focus ?? 'overall', windowMinutes: args.window_minutes ?? 30, comparePrevious: args.compare_previous ?? true }, 15_000, exec.signal),
    })),
    ctx.tools.register(defineTool({
      name: 'hgdb_monitor_query_metrics',
      description: 'Use when the question names a specific metric or time range, e.g. “过去两小时 TPS 变化”, “缓存命中相关计数”, “会话数趋势”. Queries one to eight metric keys over a 5–120 minute window. Counter metrics include cumulative points, directly usable ratePoints, reset detection, and a bounded summary. A previous-window comparison limits each window to 60 minutes; each series returns at most 240 points.',
      parameters: {
        instance_ref: { type: 'string', required: true, description: 'Anonymous instance reference returned by hgdb_monitor_list_instances.' },
        metric_keys: { type: 'array', required: true, items: { type: 'string' }, description: 'One to eight keys from: sessions_total, sessions_active, sessions_idle_transaction, sessions_waiting, long_queries, long_transactions, commits, rollbacks, blocks_hit, blocks_read, rows_inserted, rows_updated, rows_deleted, temp_files, temp_bytes, deadlocks, locks_not_granted, blocked_sessions, wal_bytes, checkpoints_timed, checkpoints_requested, replication_connections, max_replay_lag_bytes, max_replay_lag_seconds.' },
        window_minutes: { type: 'integer', description: 'Window length in minutes; 5–120; defaults to 60.' },
        end_time: { type: 'string', description: 'Optional ISO 8601 end time; defaults to now.' },
        compare_previous: { type: 'boolean', description: 'Compare with the immediately preceding equal window; defaults to false and requires window_minutes no greater than 60.' },
      }, output: jsonOutput, timeoutMs: 10_000, isConcurrencySafe: () => true,
      execute: async (args, exec) => safeCall(client, 'ai.queryMetrics', { instanceRef: args.instance_ref, metricKeys: args.metric_keys, windowMinutes: args.window_minutes ?? 60, ...(args.end_time === undefined ? {} : { endTime: args.end_time }), comparePrevious: args.compare_previous ?? false }, 10_000, exec.signal),
    })),
    ctx.tools.register(defineTool({
      name: 'hgdb_monitor_get_sessions',
      description: 'Use when the user asks who is executing what, which sessions are waiting, which statements or transactions are long, or whether anything is idle in transaction (“现在谁在执行”, “有哪些等待”, “长 SQL/长事务是什么”). Returns anonymous identities and SQL with literals removed.',
      parameters: {
        instance_ref: { type: 'string', required: true, description: 'Anonymous instance reference returned by hgdb_monitor_list_instances.' },
        filter: { type: 'string', enum: ['active', 'waiting', 'long_query', 'long_transaction', 'idle_in_transaction'], description: 'Session category; defaults to active.' },
        min_duration_seconds: { type: 'integer', description: 'Optional minimum duration in seconds; 0–86400.' },
        limit: { type: 'integer', description: 'Maximum rows; 1–20; defaults to 10.' },
      }, output: jsonOutput, timeoutMs: 15_000, isConcurrencySafe: () => true,
      execute: async (args, exec) => safeCall(client, 'ai.activities', { instanceRef: args.instance_ref, kind: args.filter ?? 'active', minDurationSeconds: args.min_duration_seconds ?? 0, limit: args.limit ?? 10 }, 15_000, exec.signal),
    })),
    ctx.tools.register(defineTool({
      name: 'hgdb_monitor_get_blocking_chains',
      description: 'Use when the user asks who blocks whom, wants a blocking chain, or needs the lock object and the waiting SQL. Returns current blocker/blocked relationships, including prepared-transaction blockers, with anonymous identities and redacted SQL. queryAgeSeconds is the age of the blocked SQL, not an exact lock-wait duration.',
      parameters: { instance_ref: { type: 'string', required: true, description: 'Anonymous instance reference returned by hgdb_monitor_list_instances.' }, limit: { type: 'integer', description: 'Maximum rows; 1–20; defaults to 10.' } },
      output: jsonOutput, timeoutMs: 15_000, isConcurrencySafe: () => true,
      execute: async (args, exec) => safeCall(client, 'ai.lockWaits', { instanceRef: args.instance_ref, limit: args.limit ?? 10 }, 15_000, exec.signal),
    })),
    ctx.tools.register(defineTool({
      name: 'hgdb_monitor_get_top_sql',
      description: 'Use when the user asks for the most time-consuming, slowest on average, most frequently called, or largest-result SQL. Reads current cumulative pg_stat_statements statistics: this is not a two-hour ranking and SQL history is not persisted.',
      parameters: { instance_ref: { type: 'string', required: true, description: 'Anonymous instance reference returned by hgdb_monitor_list_instances.' }, order_by: { type: 'string', enum: ['total_time', 'mean_time', 'calls', 'rows'], description: 'Sort order; defaults to total_time.' }, limit: { type: 'integer', description: 'Maximum rows; 1–20; defaults to 10.' } },
      output: jsonOutput, timeoutMs: 15_000, isConcurrencySafe: () => true,
      execute: async (args, exec) => safeCall(client, 'ai.topSQL', { instanceRef: args.instance_ref, order: args.order_by ?? 'total_time', limit: args.limit ?? 10 }, 15_000, exec.signal),
    })),
    ctx.tools.register(defineTool({
      name: 'hgdb_monitor_get_replication',
      description: 'Use when the user asks whether this is a primary or standby, whether replication is lagging, how downstream connections look, or how much a slot retains. Returns factual role state and anonymous replication details; read-write does not imply that a standby exists. On a standby, lastReplayAgeSeconds means time since the most recently replayed transaction, not estimated catch-up time.',
      parameters: { instance_ref: { type: 'string', required: true, description: 'Anonymous instance reference returned by hgdb_monitor_list_instances.' }, include_slots: { type: 'boolean', description: 'Include replication slots; defaults to true.' } },
      output: jsonOutput, timeoutMs: 15_000, isConcurrencySafe: () => true,
      execute: async (args, exec) => safeCall(client, 'ai.replication', { instanceRef: args.instance_ref, includeSlots: args.include_slots ?? true }, 15_000, exec.signal),
    })),
    ctx.tools.register(defineTool({
      name: 'hgdb_monitor_explain_query',
      description: 'Use when the user supplies a SELECT and asks how it would execute, whether an index could be used, or why the estimated cost is high. Produces an estimated plan only: ANALYZE is never used and the statement is not executed.',
      parameters: { instance_ref: { type: 'string', required: true, description: 'Anonymous instance reference returned by hgdb_monitor_list_instances.' }, sql: { type: 'string', required: true, description: 'One read-only statement up to 20 KB. Remove credentials and connection information before submitting.' }, format: { type: 'string', enum: ['json', 'text'], description: 'Result format; defaults to json.' } },
      output: jsonOutput, timeoutMs: 15_000, isConcurrencySafe: () => true,
      execute: async (args, exec) => safeCall(client, 'ai.explain', { instanceRef: args.instance_ref, sql: args.sql, format: args.format ?? 'json' }, 15_000, exec.signal),
    })),
  ]
  return () => { for (const dispose of disposers.reverse()) dispose() }
}
