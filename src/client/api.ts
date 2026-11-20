import type { Activity, Datasource, DatasourceInput, History, LockWait, ReplicationResult, Snapshot, StorageStatus, TopSQLResult } from './types.js'

const HEADER = { 'x-hgdb-monitor-request': '1' }

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`/hgdb-monitor/api/${path}`, {
    ...init,
    headers: { ...HEADER, ...(init?.body === undefined ? {} : { 'content-type': 'application/json' }), ...init?.headers },
  })
  const value = await response.json() as { error?: string } | T
  if (!response.ok) throw new Error('error' in (value as object) ? (value as { error?: string }).error ?? `HTTP ${response.status}` : `HTTP ${response.status}`)
  return value as T
}

export const api = {
  list: async () => (await request<Datasource[] | null>('datasources')) ?? [],
  create: (input: DatasourceInput & { password: string }) => request<Datasource>('datasources', { method: 'POST', body: JSON.stringify(input) }),
  update: (id: string, input: DatasourceInput) => request<Datasource>(`datasources/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(input) }),
  remove: (id: string) => request<{ deleted: boolean }>(`datasources/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  test: (id: string) => request<Record<string, unknown>>(`datasources/${encodeURIComponent(id)}/test`, { method: 'POST' }),
  persistent: (id: string, enabled: boolean) => request<Datasource>(`datasources/${encodeURIComponent(id)}/persistent`, { method: 'PUT', body: JSON.stringify({ enabled }) }),
  current: (id: string) => request<Snapshot>(`datasources/${encodeURIComponent(id)}/current`),
  activities: (id: string, kind = 'active', limit = 8) => request<Activity[]>(`datasources/${encodeURIComponent(id)}/activities?kind=${encodeURIComponent(kind)}&limit=${limit}`),
  lockWaits: (id: string, limit = 8) => request<LockWait[]>(`datasources/${encodeURIComponent(id)}/lock-waits?limit=${limit}`),
  latest: (id: string) => request<Snapshot>(`datasources/${encodeURIComponent(id)}/latest`),
  history: (id: string, minutes = 120) => request<History>(`datasources/${encodeURIComponent(id)}/history?minutes=${minutes}`),
  topSQL: (id: string, order = 'total_time', limit = 10) => request<TopSQLResult>(`datasources/${encodeURIComponent(id)}/top-sql?order=${encodeURIComponent(order)}&limit=${limit}`),
  replication: (id: string, includeSlots = true) => request<ReplicationResult>(`datasources/${encodeURIComponent(id)}/replication?includeSlots=${includeSlots}`),
  storage: () => request<StorageStatus>('storage'),
}

export type MonitorApi = typeof api
