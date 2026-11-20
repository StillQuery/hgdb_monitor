export interface RpcRequest {
  id: string
  method: string
  params?: unknown
}

export interface RpcError {
  code: string
  message: string
}

export interface RpcResponse<T = unknown> {
  id: string
  result?: T
  error?: RpcError
}

export interface HandshakeResult {
  protocolVersion: 1
  service: 'hgdb_monitor_core'
  capabilities: string[]
}

export interface StorageStatus {
  engine: 'sqlite'
  schemaVersion: number
  databaseReady: boolean
  snapshotCount: number
  runtime: {
    ready: boolean
    errorCode?: string
    lastCleanupAt?: string
    lastCleanupDeleted: number
  }
}

export interface DatasourceCapabilities {
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

export interface DatasourceView {
  id: string
  instanceRef: string
  displayName: string
  host: string
  port: number
  database: string
  user: string
  sslMode: 'disable' | 'allow' | 'prefer' | 'require' | 'verify-ca' | 'verify-full'
  connectTimeoutSeconds: number
  queryTimeoutSeconds: number
  capabilities: DatasourceCapabilities
  persistentEnabled: boolean
  verifiedAt?: string
  createdAt: string
  updatedAt: string
}

export interface SchedulerStatus {
  running: boolean
  lastRunAt?: string
  lastSuccessAt?: string
  lastErrorCode?: string
}
