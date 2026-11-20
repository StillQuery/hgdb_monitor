export interface Datasource {
  id: string
  instanceRef: string
  displayName: string
  host: string
  port: number
  database: string
  user: string
  sslMode: string
  connectTimeoutSeconds: number
  queryTimeoutSeconds: number
  capabilities: Capabilities
  persistentEnabled: boolean
  verifiedAt?: string
  createdAt: string
  updatedAt: string
}

export interface Capabilities {
  pgMonitor: boolean
  pgReadAllStats: boolean
  citusExtension: boolean
  pgStatStatements: boolean
  sessions: boolean
  blocking: boolean
  wal: boolean
  checkpoint: boolean
  replication: boolean
  tableStats: boolean
}

export interface Snapshot {
  collectedAt: string
  sessionsTotal: number
  sessionsActive: number
  sessionsIdleTransaction: number
  sessionsWaiting: number
  longQueries: number
  longTransactions: number
  commits: number
  rollbacks: number
  blocksHit: number
  blocksRead: number
  rowsInserted: number
  rowsUpdated: number
  rowsDeleted: number
  tempFiles: number
  tempBytes: number
  deadlocks: number
  locksNotGranted: number
  blockedSessions: number
  walBytes: number
  checkpointsTimed: number
  checkpointsRequested: number
  replicationConnections: number
  maxReplayLagBytes: number
  maxReplayLagBytesAvailable: boolean
  maxReplayLagSeconds: number
  maxReplayLagSecondsAvailable: boolean
  connected: boolean
  postmasterStart: string
  inRecovery: boolean
  maxConnections: number
  lastCollectedAt: string
}

export interface History { from: string; to: string; points: Snapshot[] }

export interface Activity {
  pid: number
  database?: string
  user?: string
  applicationName?: string
  state: string
  waitEventType?: string
  waitEvent?: string
  queryAgeSeconds: number
  transactionAgeSeconds: number
  sql: string
  backendStart?: string
  transactionStart?: string
  queryStart?: string
}

export interface LockWait {
  blockedPid: number
  blockingPid: number
  blockerType: 'session' | 'prepared_transaction'
  lockType?: string
  requestedMode?: string
  schemaName?: string
  relationName?: string
  queryAgeSeconds: number
  blockedSql: string
  blockingSql: string
}

export interface DatasourceInput {
  displayName: string
  host: string
  port: number
  database: string
  user: string
  password?: string
  sslMode: string
  connectTimeoutSeconds: number
  queryTimeoutSeconds: number
}

export interface TopSQLItem {
  queryId: string
  databaseOid: number
  userOid: number
  sql: string
  calls: number
  totalTimeMs: number
  meanTimeMs: number
  rows: number
  sharedBlocksHit: number
  sharedBlocksRead: number
  tempBlocksRead: number
  tempBlocksWritten: number
  walBytes: number
}
export interface TopSQLResult { items: TopSQLItem[]; statsReset?: string }

export interface ReplicationConnection {
  pid: number
  applicationName: string
  clientAddress?: string
  state: string
  syncState: string
  sentLsn?: string
  writeLsn?: string
  flushLsn?: string
  replayLsn?: string
  replayLagBytes: number
  replayLagBytesAvailable: boolean
  replayLagSeconds?: number
}
export interface ReplicationSlot { name: string; slotType: string; plugin?: string; database?: string; active: boolean; retainedBytes: number }
export interface ReplicationResult {
  role: 'read-write' | 'standby'
  connections: ReplicationConnection[]
  slots?: ReplicationSlot[]
  receiveLsn?: string
  replayLsn?: string
  replayLagBytes: number
  replayLagBytesAvailable: boolean
  lastReplayAgeSeconds?: number
}

export interface StorageStatus {
  engine: 'sqlite'
  schemaVersion: number
  databaseReady: boolean
  snapshotCount: number
  runtime: { ready: boolean; errorCode?: string; lastCleanupAt?: string; lastCleanupDeleted: number }
}
