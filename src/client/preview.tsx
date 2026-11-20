import { createRoot } from 'react-dom/client'
import { App } from './app.js'
import { zh } from './locales.js'
import type { MonitorApi } from './api.js'
import type { Datasource, Snapshot } from './types.js'
import './preview-theme.css'
import './style.css'

const forcedTheme = new URLSearchParams(window.location.search).get('theme')
if (forcedTheme === 'light' || forcedTheme === 'dark') document.documentElement.dataset.previewTheme = forcedTheme

const now = Date.now()
const capabilities = { pgMonitor: true, pgReadAllStats: true, citusExtension: false, pgStatStatements: true, sessions: true, blocking: true, wal: true, checkpoint: true, replication: true, tableStats: true }
const sources: Datasource[] = [
  { id: 'one', instanceRef: 'db_preview_one', displayName: '生产订单库', host: '192.0.2.21', port: 5432, database: 'orders', user: 'monitor', sslMode: 'prefer', connectTimeoutSeconds: 5, queryTimeoutSeconds: 10, capabilities, persistentEnabled: true, verifiedAt: new Date(now - 86_400_000).toISOString(), createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() },
  { id: 'two', instanceRef: 'db_preview_two', displayName: '报表分析库', host: '192.0.2.22', port: 5432, database: 'reporting', user: 'monitor', sslMode: 'require', connectTimeoutSeconds: 5, queryTimeoutSeconds: 10, capabilities, persistentEnabled: true, verifiedAt: new Date(now - 43_200_000).toISOString(), createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() },
  { id: 'three', instanceRef: 'db_preview_three', displayName: '归档库', host: '192.0.2.23', port: 5432, database: 'archive', user: 'monitor', sslMode: 'prefer', connectTimeoutSeconds: 5, queryTimeoutSeconds: 10, capabilities: { ...capabilities, pgStatStatements: false }, persistentEnabled: false, createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() },
]

function point(index: number): Snapshot {
  const wave = Math.sin(index / 8)
  const collectedAt = new Date(now - (119 - index) * 60_000).toISOString()
  return {
    collectedAt, sessionsTotal: Math.round(88 + wave * 12), sessionsActive: Math.round(13 + wave * 5),
    sessionsIdleTransaction: 2 + Math.max(0, Math.round(wave)), sessionsWaiting: Math.max(0, Math.round(2 + Math.sin(index / 5) * 2)), longQueries: 1, longTransactions: index % 30 === 0 ? 1 : 0,
    commits: 9_000_000 + index * 145 + Math.round(wave * 18), rollbacks: 20_000 + index * 2, blocksHit: 90_000_000 + index * 1000,
    blocksRead: 120_000 + index * 4, rowsInserted: 300_000 + index * 3, rowsUpdated: 400_000 + index * 2, rowsDeleted: 10_000 + index,
    tempFiles: 80 + Math.floor(index / 18), tempBytes: 1_024_000 + index * 4096, deadlocks: Math.floor(index / 70), locksNotGranted: Math.max(0, Math.round(wave)), blockedSessions: Math.max(0, Math.round(wave)), walBytes: 80_000_000 + index * 100_000,
    checkpointsTimed: 120 + Math.floor(index / 10), checkpointsRequested: 3 + Math.floor(index / 45), replicationConnections: 2, maxReplayLagBytes: Math.round(2048 + Math.abs(wave) * 8192), maxReplayLagBytesAvailable: true,
    maxReplayLagSeconds: .2 + Math.abs(wave) * .8, maxReplayLagSecondsAvailable: true, connected: true, postmasterStart: new Date(now - 604_800_000).toISOString(), inRecovery: false,
    maxConnections: 300, lastCollectedAt: collectedAt,
  }
}

const points = Array.from({ length: 120 }, (_, index) => point(index))
const api: MonitorApi = {
  list: async () => sources,
  create: async () => sources[0]!, update: async () => sources[0]!, remove: async () => ({ deleted: true }), test: async () => ({}),
  persistent: async () => sources[0]!, current: async id => ({ ...points.at(-1)!, inRecovery: id === 'two' }),
  activities: async () => Array.from({ length: 14 }, (_, index) => ({ pid: 18422 + index, database: 'orders', user: `app_user_${index + 1}`, applicationName: 'order-api', state: 'active', waitEventType: index % 2 === 0 ? 'Lock' : 'Client', waitEvent: index % 2 === 0 ? 'transactionid' : 'ClientRead', queryAgeSeconds: 18 + index * 4, transactionAgeSeconds: 80 + index * 9, sql: 'UPDATE orders SET status = $1 WHERE id = $2' })),
  lockWaits: async () => Array.from({ length: 12 }, (_, index) => ({ blockedPid: 18519 + index, blockingPid: 18422, blockerType: 'session' as const, lockType: 'relation', requestedMode: 'RowExclusiveLock', schemaName: 'public', relationName: `orders_${index + 1}`, queryAgeSeconds: 43 + index, blockedSql: 'UPDATE orders SET status = $1', blockingSql: 'UPDATE orders SET status = $1' })),
  latest: async id => ({ ...points.at(-1)!, inRecovery: id === 'two' }),
  history: async id => ({ from: points[0]!.collectedAt, to: points.at(-1)!.collectedAt, points: id === 'two' ? points.slice(-1) : points }),
  topSQL: async () => ({ statsReset: new Date(now - 86_400_000).toISOString(), items: Array.from({ length: 15 }, (_, index) => ({ queryId: String(index + 1), databaseOid: 1, userOid: index + 1, sql: 'SELECT order_id, status FROM public.orders WHERE customer_id = ?', calls: 12840 - index * 17, totalTimeMs: 82431 - index * 351, meanTimeMs: 6.42 + index / 10, rows: 12840, sharedBlocksHit: 91240, sharedBlocksRead: 840, tempBlocksRead: 0, tempBlocksWritten: 0, walBytes: 0 })) }),
  replication: async () => ({ role: 'read-write', replayLagBytes: 0, replayLagBytesAvailable: true, lastReplayAgeSeconds: 0, connections: Array.from({ length: 10 }, (_, index) => ({ pid: 2210 + index, applicationName: `standby-${index + 1}`, state: 'streaming', syncState: 'async', sentLsn: '0/8A001000', writeLsn: '0/8A000F00', flushLsn: '0/8A000F00', replayLsn: '0/8A000E00', replayLagBytes: 512 + index, replayLagBytesAvailable: true, replayLagSeconds: 0.2 + index / 10 })), slots: Array.from({ length: 8 }, (_, index) => ({ name: `standby_slot_${index + 1}`, slotType: 'physical', active: true, retainedBytes: 1024 + index })) }),
  storage: async () => ({ engine: 'sqlite', schemaVersion: 6, databaseReady: true, snapshotCount: 3250, runtime: { ready: true, lastCleanupAt: new Date(now - 3_600_000).toISOString(), lastCleanupDeleted: 120 } }),
}
const t = (key: keyof typeof zh) => zh[key]
createRoot(document.getElementById('root')!).render(<App t={t} monitorApi={api} onAnalyze={async () => { window.alert('已创建匿名分析会话') }} />)
