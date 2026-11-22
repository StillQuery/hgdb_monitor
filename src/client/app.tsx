import { useCallback, useEffect, useId, useMemo, useRef, useState, type CSSProperties, type FormEvent, type ReactNode } from 'react'
import { api as defaultApi, type MonitorApi } from './api.js'
import { cacheHit, compact, rollingRate, TrendChart } from './chart.js'
import { reportClientError } from './diagnostics.js'
import { InstanceGear } from './instance-gear.js'
import type { T } from './locales.js'
import type { Activity, Datasource, DatasourceInput, LockWait, ReplicationResult, Snapshot, StorageStatus, TopSQLResult } from './types.js'

type Page = 'overview' | 'sources'
const maxSources = 5
const motionPreferenceKey = 'hgdb_monitor.motion_mode'
const blank: DatasourceInput = { displayName: '', host: '', port: 5432, database: 'postgres', user: 'postgres', password: '', sslMode: 'prefer', connectTimeoutSeconds: 5, queryTimeoutSeconds: 10 }
const sslModes = ['disable', 'allow', 'prefer', 'require'] as const

function initialFlowMode(): boolean {
  try { return window.localStorage.getItem(motionPreferenceKey) === 'flow' }
  catch { return false }
}

function DatabaseIcon({ size = 20 }: { size?: number }) {
  return <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden>
    <ellipse cx="12" cy="5" rx="7" ry="3" /><path d="M5 5v7c0 1.7 3.1 3 7 3s7-1.3 7-3V5" /><path d="M5 12v7c0 1.7 3.1 3 7 3s7-1.3 7-3v-7" />
  </svg>
}

type FailureKey = 'failedSave' | 'failedTest' | 'failedDelete' | 'failedToggle' | 'failedLoad'

// 摘要按“用户做了什么”给出，后接后端返回的原始描述；后端文案暂不做本地化。
function failureText(t: T, action: FailureKey, error: unknown): string {
  const prefix = t(action)
  const detail = error instanceof Error ? error.message.trim() : ''
  return detail === '' ? prefix.replace(/[：:]\s*$/, '') : `${prefix}${detail}`
}

function elapsed(value: string | number | undefined): string {
  if (value === undefined) return '—'
  const timestamp = typeof value === 'number' ? value : Date.parse(value)
  const seconds = Math.max(0, Math.floor((Date.now() - timestamp) / 1000))
  if (seconds < 60) return `${seconds}s`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`
  return `${Math.floor(seconds / 3600)}h`
}

function percent(value: number): string { return `${value.toFixed(value >= 99 ? 1 : 2)}%` }
function bytes(value: number): string { return Intl.NumberFormat(undefined, { style: 'unit', unit: 'byte', notation: 'compact', maximumFractionDigits: 1 }).format(value) }
function perSecond(value: number): string { return `${compact(value)}/s` }
function bytesPerSecond(value: number): string { return `${bytes(value)}/s` }
function perMinute(value: number): string { return `${compact(value)}/min` }
function replayLatency(value: number | undefined): string { return value === undefined ? '—' : `${value.toFixed(2)}s` }
function duration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 1) return '<1s'
  if (seconds < 60) return `${Math.round(seconds)}s`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${Math.round(seconds % 60)}s`
  return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`
}

export function App({ t, monitorApi = defaultApi, onAnalyze }: { t: T; monitorApi?: MonitorApi; onAnalyze?: (instanceRef: string) => Promise<void> }) {
  const [page, setPage] = useState<Page>('overview')
  const [flowMode, setFlowMode] = useState(initialFlowMode)
  const [sources, setSources] = useState<Datasource[]>([])
  const [selected, setSelected] = useState('')
  const [displayedId, setDisplayedId] = useState('')
  const [atAddSlot, setAtAddSlot] = useState(false)
  const [sourceEditor, setSourceEditor] = useState<Datasource | 'new'>()
  const editorOpen = sourceEditor !== undefined
  const [testingId, setTestingId] = useState('')
  const [current, setCurrent] = useState<Snapshot>()
  const [points, setPoints] = useState<Snapshot[]>([])
  const [activities, setActivities] = useState<Activity[]>([])
  const [lockWaits, setLockWaits] = useState<LockWait[]>([])
  const [activityError, setActivityError] = useState(false)
  const [lockWaitError, setLockWaitError] = useState(false)
  const [topSQL, setTopSQL] = useState<TopSQLResult>()
  const [replication, setReplication] = useState<ReplicationResult>()
  const [storageStatus, setStorageStatus] = useState<StorageStatus>()
  const [loading, setLoading] = useState(true)
  const [liveLoading, setLiveLoading] = useState(false)
  const [pageUpdatedAt, setPageUpdatedAt] = useState<number>()
  const [, setDisplayClock] = useState(() => Date.now())
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [message, setMessage] = useState<{ tone: 'ok' | 'error'; text: string }>()
  const currentGeneration = useRef(0)
  const historyGeneration = useRef(0)
  const detailGeneration = useRef(0)
  const sourcesGeneration = useRef(0)

  useEffect(() => {
    try { window.localStorage.setItem(motionPreferenceKey, flowMode ? 'flow' : 'static') }
    catch { /* Local storage can be unavailable in restricted webviews. */ }
  }, [flowMode])

  const reloadSources = useCallback(async () => {
    const generation = ++sourcesGeneration.current
    const [list, storage] = await Promise.all([monitorApi.list(), monitorApi.storage().catch(() => undefined)])
    if (generation !== sourcesGeneration.current) return
    setSources(list)
    setStorageStatus(storage)
    setSelected(value => list.some(source => source.id === value) ? value : list[0]?.id ?? '')
  }, [monitorApi])

  useEffect(() => {
    setLoading(true)
    reloadSources().catch(error => {
      reportClientError('api_datasources', error, 'App')
      setMessage({ tone: 'error', text: failureText(t, 'failedLoad', error) })
    }).finally(() => setLoading(false))
  }, [reloadSources, t])

  useEffect(() => {
    if (editorOpen) return
    const timer = window.setInterval(() => {
      if (document.visibilityState === 'visible') setDisplayClock(Date.now())
    }, 1_000)
    return () => window.clearInterval(timer)
  }, [editorOpen])

  const selectedSource = sources.find(source => source.id === selected)

  useEffect(() => {
    setCurrent(undefined)
    setDisplayedId(selectedSource?.id ?? '')
    setLiveLoading(false)
    setPageUpdatedAt(undefined)
    setPoints([])
    setActivities([])
    setLockWaits([])
    setActivityError(false)
    setLockWaitError(false)
    setTopSQL(undefined)
    setReplication(undefined)
  }, [refreshNonce, selectedSource])

  useEffect(() => {
    if (editorOpen) { setLiveLoading(false); return }
    if (!selectedSource?.verifiedAt) return
    const currentToken = ++currentGeneration.current
    const historyToken = ++historyGeneration.current
    const detailToken = ++detailGeneration.current

    const loadCurrent = async () => {
      if (document.visibilityState !== 'visible') return
      setLiveLoading(true)
      try {
        let value: Snapshot
        if (selectedSource.persistentEnabled) {
          try {
            value = await monitorApi.latest(selectedSource.id)
          } catch (error) {
            reportClientError('api_latest_fallback', error, 'App')
            value = await monitorApi.current(selectedSource.id)
          }
        } else {
          value = await monitorApi.current(selectedSource.id)
        }
        if (currentGeneration.current === currentToken) {
          setCurrent(value)
          setPageUpdatedAt(Date.now())
          if (selectedSource.persistentEnabled) setPoints(previous => {
            const withoutSame = previous.filter(point => point.collectedAt !== value.collectedAt)
            return [...withoutSame, value].sort((left, right) => Date.parse(left.collectedAt) - Date.parse(right.collectedAt)).slice(-480)
          })
        }
      } catch (error) {
        reportClientError('api_current', error, 'App')
        if (currentGeneration.current === currentToken) setCurrent(undefined)
      } finally {
        if (currentGeneration.current === currentToken) setLiveLoading(false)
      }
    }
    const loadHistory = async () => {
      if (!selectedSource.persistentEnabled || document.visibilityState !== 'visible') return
      try {
        const value = await monitorApi.history(selectedSource.id, 120)
        if (historyGeneration.current === historyToken) setPoints(value.points ?? [])
      } catch (error) {
        reportClientError('api_history', error, 'App')
      }
    }

    const loadDetails = async () => {
      if (document.visibilityState !== 'visible') return
      const [activityResult, lockWaitResult] = await Promise.allSettled([
        selectedSource.capabilities.sessions ? monitorApi.activities(selectedSource.id, 'active', 8) : Promise.resolve([]),
        selectedSource.capabilities.blocking ? monitorApi.lockWaits(selectedSource.id, 8) : Promise.resolve([]),
      ])
      if (detailGeneration.current !== detailToken) return
      if (activityResult.status === 'fulfilled') {
        setActivities(activityResult.value ?? [])
        setActivityError(false)
      } else {
        reportClientError('api_activities', activityResult.reason, 'App')
        setActivities([])
        setActivityError(true)
      }
      if (lockWaitResult.status === 'fulfilled') {
        setLockWaits(lockWaitResult.value ?? [])
        setLockWaitError(false)
      } else {
        reportClientError('api_lock_waits', lockWaitResult.reason, 'App')
        setLockWaits([])
        setLockWaitError(true)
      }
    }

    const loadExtended = async () => {
      if (document.visibilityState !== 'visible') return
      const [sqlResult, replicationResult] = await Promise.all([
        selectedSource.capabilities.pgStatStatements ? monitorApi.topSQL(selectedSource.id).catch(error => { reportClientError('api_top_sql', error, 'App'); return undefined }) : Promise.resolve(undefined),
        selectedSource.capabilities.replication ? monitorApi.replication(selectedSource.id).catch(error => { reportClientError('api_replication', error, 'App'); return undefined }) : Promise.resolve(undefined),
      ])
      if (detailGeneration.current === detailToken) {
        setTopSQL(sqlResult)
        setReplication(replicationResult)
      }
    }

    void Promise.all([loadCurrent(), loadHistory(), loadDetails(), loadExtended()])
    let cycle = 0
    const timer = window.setInterval(() => {
      cycle += 1
      void Promise.all([loadCurrent(), ...(cycle % 2 === 0 ? [loadDetails()] : []), ...(cycle % 4 === 0 ? [loadHistory(), loadExtended()] : [])])
    }, 15_000)
    return () => {
      window.clearInterval(timer)
      currentGeneration.current += 1
      historyGeneration.current += 1
      detailGeneration.current += 1
    }
  }, [editorOpen, monitorApi, refreshNonce, selectedSource])

  const notify = (tone: 'ok' | 'error', text: string) => { setMessage({ tone, text }); window.setTimeout(() => setMessage(undefined), 3200) }
  const refresh = () => {
    void reloadSources().catch(error => reportClientError('api_refresh_sources', error, 'App'))
    setRefreshNonce(value => value + 1)
  }
  const analyze = onAnalyze === undefined ? undefined : async (instanceRef: string) => {
    try { await onAnalyze(instanceRef); notify('ok', t('analysisStarted')) }
    catch (error) { reportClientError('start_analysis', error, 'App'); notify('error', t('operationFailed')) }
  }

  const configure = () => { setAtAddSlot(false); setPage('sources') }
  const hasAddSlot = !loading && sources.length < maxSources
  const addSlot = hasAddSlot ? sources.length : -1
  const selectedIndex = sources.findIndex(source => source.id === selected)
  const showingAdd = hasAddSlot && (atAddSlot || selectedIndex < 0)
  const hasCurrentSource = displayedId === selected
  const station = showingAdd ? t('addSourceSlot') : selectedIndex < 0 ? t('instanceOverview') : t('gearPrompt')
  const selectSlot = (slot: number) => {
    setAtAddSlot(slot === addSlot)
    const source = sources[slot]
    if (source) setSelected(source.id)
  }
  const testSource = async (id: string) => {
    setTestingId(id)
    try { await monitorApi.test(id); await reloadSources(); notify('ok', t('tested')) }
    catch (error) { reportClientError('datasource_test', error, 'App'); notify('error', failureText(t, 'failedTest', error)) }
    finally { setTestingId('') }
  }
  const metaAction = showingAdd
    ? <button type="button" className="hgm-instance-action" onClick={() => setSourceEditor('new')}>＋ {t('addSource')}</button>
    : selectedSource && !selectedSource.verifiedAt
      ? <button type="button" className="hgm-instance-action" disabled={testingId !== ''} onClick={() => void testSource(selectedSource.id)}>{testingId === selectedSource.id ? t('testing') : t('test')}</button>
      : undefined
  const gear = <InstanceGear count={sources.length + Number(hasAddSlot)} slot={showingAdd ? addSlot : Math.max(0, selectedIndex)}
    label={`${t('selectInstance')} · ${showingAdd ? station : selectedSource?.displayName ?? station}. ${t('gearHint')}`} hint={t('gearHint')}
    onSelect={selectSlot} />

  return <main className="hgm-page" data-motion={flowMode ? 'flow' : 'static'}>
    <div className="hgm-main-scroll">
      <header className="hgm-toolbar">
        <div className="hgm-toolbar-inner">
          <div className="hgm-toolbar-left">
            {page === 'overview'
              ? <span className="hgm-toolbar-title">{t('overview')}</span>
              : <button className="hgm-toolbar-link" onClick={() => setPage('overview')}>← {t('overview')}</button>}
          </div>
          <div className="hgm-topbar-meta">
            {page === 'overview' && <><span>{t('lastSample')} <b>{hasCurrentSource && liveLoading ? '…' : elapsed(hasCurrentSource ? pageUpdatedAt : undefined)}</b>{hasCurrentSource && !liveLoading && pageUpdatedAt !== undefined ? <> {t('ago')}</> : null}</span><button className="hgm-toolbar-link" onClick={configure}>{t('manageSources')}</button></>}
            <button className="hgm-motion-toggle" aria-pressed={flowMode} onClick={() => setFlowMode(value => !value)} title={t('motionMode')}><i aria-hidden /><span>{t('motionMode')}</span></button>
            <button className="hgm-icon-btn" onClick={refresh} title={t('refresh')} aria-label={t('refresh')}><svg viewBox="0 0 24 24" aria-hidden><path d="M20 6v5h-5M4 18v-5h5" /><path d="M18.2 9A7 7 0 0 0 6.5 6.5L4 11m16 2-2.5 4.5A7 7 0 0 1 5.8 15" /></svg></button>
          </div>
        </div>
      </header>
      {message && <div className="hgm-toast" data-tone={message.tone}>{message.text}</div>}
      {page === 'overview'
        ? <Overview t={t} source={selectedSource} current={hasCurrentSource ? current : undefined} points={hasCurrentSource ? points : []} activities={hasCurrentSource ? activities : []} lockWaits={hasCurrentSource ? lockWaits : []} activityError={hasCurrentSource && activityError} lockWaitError={hasCurrentSource && lockWaitError} topSQL={hasCurrentSource ? topSQL : undefined} replication={hasCurrentSource ? replication : undefined} empty={!loading && sources.length === 0} station={station} metaAction={metaAction} gear={gear} onConfigure={configure} {...(analyze === undefined ? {} : { onAnalyze: analyze })} />
        : <Sources t={t} sources={sources} storageStatus={storageStatus} monitorApi={monitorApi} reload={reloadSources} notify={notify} onEdit={setSourceEditor} />}
    </div>
    {sourceEditor && <SourceDialog t={t} source={sourceEditor === 'new' ? undefined : sourceEditor} close={() => setSourceEditor(undefined)} save={async input => {
      const isNew = sourceEditor === 'new'
      const source = isNew ? await monitorApi.create({ ...input, password: input.password ?? '' }) : await monitorApi.update(sourceEditor.id, input)
      sourcesGeneration.current += 1
      setSources(list => list.some(item => item.id === source.id) ? list.map(item => item.id === source.id ? source : item) : [...list, source])
      if (isNew && (page === 'overview' || selectedSource === undefined)) {
        setSelected(source.id)
        setAtAddSlot(false)
      }
      setSourceEditor(undefined)
      notify('ok', t('saved'))
    }} />}
  </main>
}

function Overview({ t, source, current, points, activities, lockWaits, activityError, lockWaitError, topSQL, replication, empty, station, metaAction, gear, onConfigure, onAnalyze }: {
  t: T; source: Datasource | undefined; current: Snapshot | undefined; points: Snapshot[]; activities: Activity[]; lockWaits: LockWait[]; activityError: boolean; lockWaitError: boolean; topSQL: TopSQLResult | undefined; replication: ReplicationResult | undefined; empty: boolean; station: string; metaAction: ReactNode; gear: ReactNode; onConfigure(): void; onAnalyze?: (instanceRef: string) => Promise<void>
}) {
  const utilization = current && current.maxConnections > 0 ? current.sessionsTotal * 100 / current.maxConnections : undefined
  const commitRates = rollingRate(points, 'commits')
  const rollbackRates = rollingRate(points, 'rollbacks')
  const latestCommitRate = commitRates.at(-1)
  const latestRollbackRate = rollbackRates.at(-1)
  const transactionRate = points.length > 1 && Number.isFinite(latestCommitRate) && Number.isFinite(latestRollbackRate)
    ? latestCommitRate! + latestRollbackRate!
    : undefined
  const uptime = current?.postmasterStart ? duration((Date.now() - Date.parse(current.postmasterStart)) / 1000) : '—'
  return <section className="hgm-content" id="hgm-overview">
    <div className="hgm-cockpit-head">
      <div className="hgm-instance-title">
        {gear}
        <div className="hgm-instance-caption" aria-live="polite"><p className="hgm-eyebrow hgm-instance-station">{station}</p><h2 title={source?.displayName}>{source?.displayName ?? t(empty ? 'noSources' : 'instance')}</h2><div className="hgm-instance-meta">{metaAction ?? <><span>{current ? (current.inRecovery ? t('standby') : t('readWrite')) : '—'}</span><i /> <span>{t('uptime')} {uptime}</span></>}</div></div>
      </div>
      {!empty && <div className="hgm-heading-actions">
        <div className="hgm-head-signals"><span data-on={current?.connected}><i />{current?.connected ? t('connected') : source?.verifiedAt ? t('realtimeUnavailable') : t('unverified')}</span><span data-on={source?.persistentEnabled}><i />{source?.persistentEnabled ? t('collecting') : t('paused')}</span></div>
        {source && onAnalyze && <button className="hgm-ai-button" onClick={() => void onAnalyze(source.instanceRef)}><span>✦</span>{t('analyzeWithAI')}</button>}
      </div>}
    </div>
    {empty ? <p className="hgm-overview-empty">{t('noSourcesHint')}</p> : <>
    <div className="hgm-kpis">
      <Kpi code="CONN" label={t('sessions')} value={current?.sessionsTotal ?? '—'} suffix={current ? `/ ${current.maxConnections}` : undefined} detail={<span>{t('connectionUsage')} {utilization === undefined ? '—' : percent(utilization)}</span>} progress={utilization} tone="blue" />
      <Kpi code="ACTV" label={t('activeSessionList')} value={current?.sessionsActive ?? '—'} detail={<><span>{t('waitingSessions')} {current?.sessionsWaiting ?? '—'}</span><span>{t('idleTransaction')} {current?.sessionsIdleTransaction ?? '—'}</span></>} tone="cyan" />
      <Kpi code="LOCK" label={t('blockedSessions')} value={current?.blockedSessions ?? '—'} detail={<><span>{t('waitingSessions')} {current?.sessionsWaiting ?? '—'}</span><span>{t('locks')} {current?.locksNotGranted ?? '—'}</span></>} tone="amber" />
      <Kpi code="TPS" label={t('transactions')} value={transactionRate === undefined ? '—' : compact(transactionRate)} suffix={transactionRate === undefined ? undefined : '/s'} detail={<><span>commit {Number.isFinite(latestCommitRate) ? perSecond(latestCommitRate!) : '—'}</span><span>rollback {Number.isFinite(latestRollbackRate) ? perSecond(latestRollbackRate!) : '—'}</span></>} tone="violet" />
    </div>
    <OverviewDashboard t={t} source={source} current={current} points={points} />
    <div id="hgm-trends"><TrendArea t={t} source={source} current={current} points={points} onConfigure={onConfigure} /></div>
    <div className="hgm-operations-grid">
      <div id="hgm-activity"><ActivityTable t={t} items={activities} available={source?.capabilities.sessions === true} failed={activityError} /></div>
      <div id="hgm-locks"><LockTopology t={t} items={lockWaits} available={source?.capabilities.blocking === true} failed={lockWaitError} /></div>
    </div>
    <div className="hgm-operations-grid hgm-extended-grid">
      <div id="hgm-top-sql"><TopSQLPanel t={t} source={source} result={topSQL} /></div>
      <ReplicationPanel t={t} source={source} result={replication} />
    </div>
    </>}
  </section>
}

function OverviewDashboard({ t, source, current, points }: { t: T; source: Datasource | undefined; current: Snapshot | undefined; points: Snapshot[] }) {
  const latestCacheHit = points.length > 1 ? cacheHit(points).at(-1) : undefined
  const walRate = points.length > 1 ? rollingRate(points, 'walBytes').at(-1) : undefined
  const hasHistory = source?.persistentEnabled === true && points.length > 1
  return <div className="hgm-dashboard-deck">
    <div className="hgm-dashboard-row">
      <article className="hgm-primary-trend">
        <header><div><h3>{t('connectionTrend')}</h3><p>{t('twoHours')} · 15s</p></div><span data-on={source?.persistentEnabled}>{source?.persistentEnabled ? t('collecting') : t('paused')}</span></header>
        {hasHistory ? <TrendChart points={points} series={[
          { label: t('totalSessions'), color: 'var(--hgm-blue)', values: points.map(point => point.sessionsTotal) },
          { label: t('activeSessions'), color: 'var(--hgm-cyan)', values: points.map(point => point.sessionsActive) },
        ]} /> : <div className="hgm-primary-empty"><b>{source?.persistentEnabled ? t('noHistory') : t('collectionDisabled')}</b><span>{t('historyHint')}</span></div>}
      </article>
      <article className="hgm-snapshot-panel">
        <header><h3>{t('factsSummary')}</h3><span>{t('currentFacts')}</span></header>
        <div className="hgm-snapshot-core">
          <CapacityGauge value={current && current.maxConnections > 0 ? current.sessionsTotal * 100 / current.maxConnections : undefined} label={t('connectionUsage')} />
          <div className="hgm-snapshot-facts">
            <SnapshotRow label={t('longQueries')} value={current?.longQueries ?? '—'} detail={`${t('longTransactions')} ${current?.longTransactions ?? '—'}`} tone="violet" />
            <SnapshotRow label={t('blockingTopology')} value={current?.blockedSessions ?? '—'} detail={`${t('locks')} ${current?.locksNotGranted ?? '—'}`} tone="amber" />
            <SnapshotRow label={t('replicationDetails')} value={current?.replicationConnections ?? '—'} detail={(current?.replicationConnections ?? 0) > 0 ? `${t('replicationLag')} ${current!.maxReplayLagBytesAvailable ? bytes(current!.maxReplayLagBytes) : '—'}` : t('noDownstream')} tone="cyan" />
          </div>
        </div>
      </article>
    </div>
    <div className="hgm-summary-metrics">
      <svg className="hgm-telemetry-wave" viewBox="0 0 1200 86" preserveAspectRatio="none" aria-hidden="true">
        <defs>
          <linearGradient id="hgm-wave-stroke" x1="0" y1="0" x2="1" y2="0">
            <stop offset="0" stopColor="var(--hgm-cyan)" />
            <stop offset=".52" stopColor="var(--hgm-blue)" />
            <stop offset="1" stopColor="var(--hgm-violet)" />
          </linearGradient>
          <linearGradient id="hgm-wave-fill" x1="0" y1="0" x2="1" y2="0">
            <stop offset="0" stopColor="var(--hgm-cyan)" stopOpacity=".09" />
            <stop offset=".52" stopColor="var(--hgm-blue)" stopOpacity=".07" />
            <stop offset="1" stopColor="var(--hgm-violet)" stopOpacity=".09" />
          </linearGradient>
        </defs>
        <path className="hgm-wave-area" fill="url(#hgm-wave-fill)" d="M0 75 C90 67 140 81 230 74 S380 62 470 73 S620 83 720 71 S880 62 970 73 S1110 83 1200 67 L1200 86 L0 86 Z" />
        <path className="hgm-wave-line" fill="none" stroke="url(#hgm-wave-stroke)" d="M0 75 C90 67 140 81 230 74 S380 62 470 73 S620 83 720 71 S880 62 970 73 S1110 83 1200 67" />
      </svg>
      <MiniMetric code="CACHE" label={t('cacheHit')} value={latestCacheHit === undefined || !Number.isFinite(latestCacheHit) ? '—' : percent(latestCacheHit)} detail="60s" tone="cyan" />
      <MiniMetric code="WAL" label={t('walThroughput')} value={walRate === undefined || !Number.isFinite(walRate) ? '—' : bytesPerSecond(walRate)} detail={t('twoHours')} tone="blue" />
      <MiniMetric code="REPL" label={t('replicationLag')} value={(current?.replicationConnections ?? 0) > 0 && current?.maxReplayLagBytesAvailable ? bytes(current.maxReplayLagBytes) : '—'} detail={(current?.replicationConnections ?? 0) > 0 ? `${t('recentReplayLatency')} ${current!.maxReplayLagSecondsAvailable ? `${current!.maxReplayLagSeconds.toFixed(2)}s` : '—'}` : t('noDownstream')} tone="violet" />
    </div>
  </div>
}

function CapacityGauge({ value, label }: { value: number | undefined; label: string }) {
  const bounded = value === undefined || !Number.isFinite(value) ? 0 : Math.min(100, Math.max(0, value))
  return <div className="hgm-capacity-gauge" style={{ '--hgm-gauge': `${bounded * 3.6}deg` } as CSSProperties}>
    <div><strong>{value === undefined ? '—' : percent(value)}</strong><span>{label}</span></div>
  </div>
}

function SnapshotRow({ label, value, detail, tone }: { label: string; value: string | number; detail: string; tone: string }) {
  return <div className="hgm-snapshot-row" data-tone={tone}><i /><div><b>{label}</b><span>{detail}</span></div><strong className="hgm-value-refresh" key={value}>{value}</strong></div>
}

function MiniMetric({ code, label, value, detail, tone }: { code: string; label: string; value: string; detail: string; tone: string }) {
  return <article className="hgm-mini-metric" data-tone={tone}>
    <div className="hgm-mini-signal"><i /><code>{code}</code></div>
    <div className="hgm-mini-copy"><span>{label}</span><small>{detail}</small></div>
    <strong className="hgm-value-refresh" key={value}>{value}</strong>
  </article>
}

function TrendArea({ t, source, current, points, onConfigure }: { t: T; source: Datasource | undefined; current: Snapshot | undefined; points: Snapshot[]; onConfigure(): void }) {
  const [view, setView] = useState<'sessions' | 'io' | 'writes' | 'stability'>('sessions')
  if (source?.persistentEnabled !== true) return <HistoryState t={t} mode="disabled" title={t('collectionDisabled')} detail={t('historyDisabledHint')} onConfigure={onConfigure} />
  if (points.length < 2) return <HistoryState t={t} mode="warming" title={t('noHistory')} detail={t('historyBuildingHint')} sampleCount={points.length} />

  const commitRate = rollingRate(points, 'commits')
  const rollbackRate = rollingRate(points, 'rollbacks')
  const insertRate = rollingRate(points, 'rowsInserted')
  const updateRate = rollingRate(points, 'rowsUpdated')
  const deleteRate = rollingRate(points, 'rowsDeleted')
  const readRate = rollingRate(points, 'blocksRead')
  const walRate = rollingRate(points, 'walBytes')
  const tempRate = rollingRate(points, 'tempBytes')
  const timedPerMinute = rollingRate(points, 'checkpointsTimed').map(value => value * 60)
  const requestedPerMinute = rollingRate(points, 'checkpointsRequested').map(value => value * 60)
  const deadlocksPerMinute = rollingRate(points, 'deadlocks').map(value => value * 60)
  const showReplication = (current?.replicationConnections ?? 0) > 0 || points.some(point => point.replicationConnections > 0)

  return <div className="hgm-trends">
    <div className="hgm-trends-title"><div><p className="hgm-eyebrow">{t('historyTrends')}</p><h2>{t('twoHours')}</h2></div><div className="hgm-trend-tabs">
      {([['sessions', t('trendSessions')], ['io', t('trendIO')], ['writes', t('trendWrites')], ['stability', t('trendStability')]] as const).map(([key, label]) => <button key={key} aria-pressed={view === key} onClick={() => setView(key)}>{label}</button>)}
    </div></div>
    <div className="hgm-trend-grid">
      {view === 'sessions' && <><ChartCard title={t('sessions')} points={points} series={[
        { label: t('totalSessions'), color: 'var(--hgm-blue)', values: points.map(point => point.sessionsTotal) },
        { label: t('activeSessions'), color: 'var(--hgm-cyan)', values: points.map(point => point.sessionsActive) },
        { label: t('waitingSessions'), color: 'var(--hgm-amber)', values: points.map(point => point.sessionsWaiting) },
        { label: t('idleTransaction'), color: 'var(--hgm-violet)', values: points.map(point => point.sessionsIdleTransaction) },
      ]} />
      <ChartCard title={t('transactionLoad')} points={points} series={[
        { label: 'commit', color: 'var(--hgm-violet)', values: commitRate },
        { label: 'rollback', color: 'var(--hgm-red)', values: rollbackRate },
      ]} format={perSecond} /></>}
      {view === 'io' && <><ChartCard title={t('cacheHit')} points={points} series={[{ label: t('cacheHit'), color: 'var(--hgm-cyan)', values: cacheHit(points) }]} format={percent} />
      <ChartCard title={t('blocksRead')} points={points} series={[{ label: t('blocksRead'), color: 'var(--hgm-blue)', values: readRate }]} format={perSecond} /></>}
      {view === 'writes' && <><ChartCard title={t('dataChanges')} points={points} series={[
        { label: t('rowsInserted'), color: 'var(--hgm-cyan)', values: insertRate },
        { label: t('rowsUpdated'), color: 'var(--hgm-violet)', values: updateRate },
        { label: t('rowsDeleted'), color: 'var(--hgm-red)', values: deleteRate },
      ]} format={perSecond} />
      <ChartCard title={t('walThroughput')} points={points} series={[{ label: 'WAL/s', color: 'var(--hgm-amber)', values: walRate }]} format={bytesPerSecond} />
      <ChartCard title={t('tempThroughput')} points={points} series={[{ label: t('tempThroughput'), color: 'var(--hgm-blue)', values: tempRate }]} format={bytesPerSecond} /></>}
      {view === 'stability' && <><ChartCard title={t('checkpointTrend')} points={points} series={[
        { label: t('checkpointTimed'), color: 'var(--hgm-cyan)', values: timedPerMinute },
        { label: t('checkpointRequested'), color: 'var(--hgm-amber)', values: requestedPerMinute },
      ]} format={perMinute} />
      <ChartCard title={t('contentionTrend')} points={points} series={[
        { label: t('blockedSessions'), color: 'var(--hgm-red)', values: points.map(point => point.blockedSessions) },
        { label: t('locks'), color: 'var(--hgm-amber)', values: points.map(point => point.locksNotGranted) },
        { label: t('deadlocks'), color: 'var(--hgm-violet)', values: deadlocksPerMinute },
      ]} />
      {showReplication && <ChartCard title={t('recentReplayLatency')} points={points} series={[{ label: 'seconds', color: 'var(--hgm-amber)', values: points.map(point => point.maxReplayLagSecondsAvailable ? point.maxReplayLagSeconds : Number.NaN) }]} />}</>}
    </div>
  </div>
}

function HistoryState({ t, mode, title, detail, sampleCount, onConfigure }: { t: T; mode: 'disabled' | 'warming'; title: string; detail: string; sampleCount?: number; onConfigure?: () => void }) {
  return <section className="hgm-history-state" data-mode={mode}>
    <span aria-hidden>{mode === 'disabled' ? '↗' : '···'}</span>
    <div className="hgm-history-state-copy"><p className="hgm-eyebrow">{t('historyTrends')}</p><h3>{title}</h3><p>{detail}</p></div>
    {mode === 'warming' && <small className="hgm-history-samples">{t('sampleCount')} {sampleCount ?? 0} / 2</small>}
    {mode === 'disabled' && onConfigure && <button className="hgm-history-action" onClick={onConfigure}>{t('configureCollection')}</button>}
  </section>
}

function ActivityTable({ t, items, available, failed }: { t: T; items: Activity[]; available: boolean; failed: boolean }) {
  return <section className="hgm-detail-panel hgm-activity-panel">
    <header><div><p className="hgm-eyebrow">{t('currentEvents')}</p><h3>{t('activeSessionList')}</h3></div><span>{failed || !available ? '—' : items.length}</span></header>
    {!available ? <div className="hgm-detail-empty">{t('capabilityUnavailable')}</div> : failed ? <div className="hgm-detail-empty">{t('detailLoadFailed')}</div> : items.length === 0 ? <div className="hgm-detail-empty">{t('noActiveSessions')}</div> : <div className="hgm-table-scroll"><table>
      <thead><tr><th>{t('pid')}</th><th>{t('database')}</th><th>{t('user')}</th><th>{t('application')}</th><th>{t('state')}</th><th>{t('waitEvent')}</th><th>{t('queryDuration')}</th><th>{t('querySummary')}</th></tr></thead>
      <tbody>{items.map(item => <tr key={item.pid}>
        <td><code>{item.pid}</code></td><td>{item.database ?? '—'}</td><td>{item.user ?? '—'}</td><td>{item.applicationName ?? '—'}</td><td>{item.state}</td><td>{item.waitEvent ?? item.waitEventType ?? '—'}</td>
        <td>{duration(item.queryAgeSeconds)}</td><td title={item.sql}><code>{item.sql || '—'}</code></td>
      </tr>)}</tbody>
    </table></div>}
  </section>
}

function LockTopology({ t, items, available, failed }: { t: T; items: LockWait[]; available: boolean; failed: boolean }) {
  return <section className="hgm-detail-panel hgm-lock-panel">
    <header><div><p className="hgm-eyebrow">{t('currentEvents')}</p><h3>{t('blockingTopology')}</h3></div><span>{failed || !available ? '—' : items.length}</span></header>
    {!available ? <div className="hgm-detail-empty">{t('capabilityUnavailable')}</div> : failed ? <div className="hgm-detail-empty">{t('detailLoadFailed')}</div> : items.length === 0 ? <div className="hgm-detail-empty">{t('noLockWaits')}</div> : <div className="hgm-lock-list">{items.map((item, index) => {
      const object = [item.schemaName, item.relationName].filter(Boolean).join('.') || item.lockType || '—'
      const blocker = item.blockerType === 'prepared_transaction' ? t('preparedTransaction') : `PID ${item.blockingPid}`
      return <article key={`${item.blockerType}-${item.blockingPid}-${item.blockedPid}-${index}`}>
        <div className="hgm-lock-chain"><span><small>{t('blocker')}</small><b>{blocker}</b></span><i>→</i><span><small>{t('blocked')}</small><b>PID {item.blockedPid}</b></span></div>
        <dl><div><dt>{t('queryDuration')}</dt><dd>{duration(item.queryAgeSeconds)}</dd></div><div><dt>{t('lockObject')}</dt><dd title={object}>{object}</dd></div><div><dt>{t('lockMode')}</dt><dd>{item.requestedMode ?? '—'}</dd></div></dl>
      </article>
    })}</div>}
  </section>
}

function TopSQLPanel({ t, source, result }: { t: T; source: Datasource | undefined; result: TopSQLResult | undefined }) {
  const available = source?.capabilities.pgStatStatements === true
  return <section className="hgm-detail-panel hgm-wide-table-panel">
    <header><div><p className="hgm-eyebrow">{t('currentFacts')}</p><h3>{t('topSQL')}</h3></div><span>{result?.items.length ?? 0}</span></header>
    {!available ? <div className="hgm-detail-empty">{t('topSQLUnavailable')}</div> : result === undefined ? <div className="hgm-detail-empty">—</div> : result.items.length === 0 ? <div className="hgm-detail-empty">{t('noTopSQL')}</div> : <div className="hgm-table-scroll"><table>
      <thead><tr><th>{t('querySummary')}</th><th>{t('calls')}</th><th>{t('totalTime')}</th><th>{t('meanTime')}</th><th>{t('rows')}</th><th>{t('blockIo')}</th><th>WAL</th></tr></thead>
      <tbody>{result.items.map(item => <tr key={`${item.databaseOid}-${item.userOid}-${item.queryId}`}>
        <td title={item.sql}><code>{item.sql || '—'}</code></td><td>{compact(item.calls)}</td><td>{compact(item.totalTimeMs)} ms</td><td>{item.meanTimeMs.toFixed(2)} ms</td><td>{compact(item.rows)}</td><td>{compact(item.sharedBlocksHit)} / {compact(item.sharedBlocksRead)}</td><td>{bytes(item.walBytes)}</td>
      </tr>)}</tbody>
    </table></div>}
    {result?.statsReset && <footer className="hgm-panel-foot">{t('statsReset')} {elapsed(result.statsReset)}</footer>}
  </section>
}

function ReplicationPanel({ t, source, result }: { t: T; source: Datasource | undefined; result: ReplicationResult | undefined }) {
  const available = source?.capabilities.replication === true
  return <section className="hgm-detail-panel hgm-replication-panel">
    <header><div><p className="hgm-eyebrow">{t('currentFacts')}</p><h3>{t('replicationDetails')}</h3></div><span>{result === undefined ? '—' : t(result.role === 'standby' ? 'standby' : 'readWrite')}</span></header>
    {!available ? <div className="hgm-detail-empty">{t('replicationUnavailable')}</div> : result === undefined ? <div className="hgm-detail-empty">—</div> : <div className="hgm-replication-body">{result.role === 'standby' ? <div className="hgm-replica-summary">
      <Metric label="receive LSN" value={result.receiveLsn ?? '—'} /><Metric label="replay LSN" value={result.replayLsn ?? '—'} />
      <Metric label={t('replicationLag')} value={`${result.replayLagBytesAvailable ? bytes(result.replayLagBytes) : '—'} · ${t('lastReplayAge')} ${replayLatency(result.lastReplayAgeSeconds)}`} />
    </div> : <>
      {result.connections.length === 0 ? <div className="hgm-detail-empty">{t('noDownstream')}</div> : <div className="hgm-replica-list">{result.connections.map(item => <article key={item.pid}>
        <div><b>{item.applicationName || `PID ${item.pid}`}</b><span>{item.state} · {item.syncState}</span></div>
        <code>{item.sentLsn ?? '—'} → {item.replayLsn ?? '—'}</code><strong>{item.replayLagBytesAvailable ? bytes(item.replayLagBytes) : '—'} · {replayLatency(item.replayLagSeconds)}</strong>
      </article>)}</div>}
      {(result.slots?.length ?? 0) > 0 && <div className="hgm-slot-list">{result.slots!.map(slot => <span key={slot.name}><b>{slot.name}</b>{slot.slotType} · {slot.active ? t('active') : t('inactive')} · {bytes(slot.retainedBytes)}</span>)}</div>}
    </>}</div>}
  </section>
}

function Metric({ label, value }: { label: string; value: string }) { return <div className="hgm-metric"><span>{label}</span><strong>{value}</strong></div> }

function Kpi({ code, label, value, suffix, detail, tone, progress }: { code: string; label: string; value: string | number; suffix?: string | undefined; detail: ReactNode; tone: string; progress?: number | undefined }) {
  const width = progress === undefined || !Number.isFinite(progress) ? undefined : Math.min(100, Math.max(0, progress))
  return <article className="hgm-kpi" data-tone={tone}><div className="hgm-kpi-top"><span>{label}</span><code>{code}</code></div><strong className="hgm-value-refresh" key={value}>{value}{suffix && <small>{suffix}</small>}</strong><p className="hgm-kpi-details">{detail}</p>{width !== undefined && <div className="hgm-kpi-meter"><i style={{ width: `${width}%` }} /></div>}<i className="hgm-kpi-glow" /></article>
}

function ChartCard({ title, points, series, format }: Parameters<typeof TrendChart>[0] & { title: string }) {
  return <article className="hgm-panel"><header><h3>{title}</h3><span>15 s</span></header><TrendChart points={points} series={series} {...(format === undefined ? {} : { format })} /></article>
}

function Sources({ t, sources, storageStatus, monitorApi, reload, notify, onEdit }: { t: T; sources: Datasource[]; storageStatus: StorageStatus | undefined; monitorApi: MonitorApi; reload(): Promise<void>; notify(tone: 'ok' | 'error', text: string): void; onEdit(source: Datasource | 'new'): void }) {
  const [busy, setBusy] = useState('')
  const act = async (key: string, work: () => Promise<unknown>, success?: string, failure: FailureKey = 'failedSave') => {
    setBusy(key)
    try { await work(); await reload(); if (success) notify('ok', success) }
    catch (error) { reportClientError('datasource_action', error, 'Sources'); notify('error', failureText(t, failure, error)) }
    finally { setBusy('') }
  }
  return <section className="hgm-content">
    <div className="hgm-source-head"><div><p className="hgm-eyebrow">{t('sourceLimit')}</p><h2>{t('sources')}</h2></div><button className="hgm-primary" disabled={sources.length >= maxSources} onClick={() => onEdit('new')}>＋ {t('addSource')}</button></div>
    {sources.length > 0 && <><StorageCard t={t} status={storageStatus} />
    <div className="hgm-source-list">
      {sources.map(source => <article className="hgm-source-card" key={source.id}>
        <div className="hgm-source-icon"><DatabaseIcon /></div>
        <div className="hgm-source-main"><h3>{source.displayName}</h3><p>{source.host}:{source.port} · {source.database} · {source.user}</p><div><span data-on={source.verifiedAt !== undefined}>{source.verifiedAt ? t('verified') : t('unverified')}</span><span>{source.sslMode}</span><span>{t('timeouts')} {source.connectTimeoutSeconds}s / {source.queryTimeoutSeconds}s</span></div><CapabilityBadges t={t} source={source} /></div>
        <label className="hgm-toggle-row"><span><b>{t('persistent')}</b><small>{t('persistentHint')}</small></span><button className="hgm-switch" role="switch" aria-checked={source.persistentEnabled} disabled={!source.verifiedAt || busy !== ''} onClick={() => void act(`persistent-${source.id}`, () => monitorApi.persistent(source.id, !source.persistentEnabled), undefined, 'failedToggle')}><i /></button></label>
        <div className="hgm-source-actions">
          <button disabled={busy !== ''} onClick={() => void act(`test-${source.id}`, () => monitorApi.test(source.id), t('tested'), 'failedTest')}>{busy === `test-${source.id}` ? t('testing') : t('test')}</button>
          <button disabled={busy !== ''} onClick={() => onEdit(source)}>{t('edit')}</button>
          <button className="hgm-danger" disabled={busy !== ''} onClick={() => { if (window.confirm(t('confirmDelete'))) void act(`delete-${source.id}`, () => monitorApi.remove(source.id), undefined, 'failedDelete') }}>{t('delete')}</button>
        </div>
      </article>)}
    </div></>}
  </section>
}

function StorageCard({ t, status }: { t: T; status: StorageStatus | undefined }) {
  return <section className="hgm-storage-card"><div><p className="hgm-eyebrow">SQLite</p><h3>{t('localStorage')}</h3></div><div className="hgm-storage-metrics">
    <Metric label={t('storageStatus')} value={status === undefined ? '—' : status.databaseReady && status.runtime.ready ? t('available') : t('unavailable')} />
    <Metric label={t('sampleCount')} value={status === undefined ? '—' : compact(status.snapshotCount)} />
    <Metric label={t('lastCleanup')} value={status?.runtime.lastCleanupAt ? elapsed(status.runtime.lastCleanupAt) : '—'} />
  </div></section>
}

function CapabilityBadges({ t, source }: { t: T; source: Datasource }) {
  if (source.verifiedAt === undefined) return null
  const entries = [[t('sessions'), source.capabilities.sessions], [t('blockingTopology'), source.capabilities.blocking], ['WAL', source.capabilities.wal], [t('topSQL'), source.capabilities.pgStatStatements], [t('replicationDetails'), source.capabilities.replication]] as const
  return <div className="hgm-capabilities">{entries.map(([label, enabled]) => <span key={label} data-on={enabled}>{label}</span>)}</div>
}

function SourceDialog({ t, source, close, save }: { t: T; source: Datasource | undefined; close(): void; save(input: DatasourceInput): Promise<void> }) {
  const initial = useMemo<DatasourceInput>(() => source ? { displayName: source.displayName, host: source.host, port: source.port, database: source.database, user: source.user, password: '', sslMode: (sslModes as readonly string[]).includes(source.sslMode) ? source.sslMode : 'require', connectTimeoutSeconds: source.connectTimeoutSeconds, queryTimeoutSeconds: source.queryTimeoutSeconds } : blank, [source])
  const [form, setForm] = useState(initial)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const dialog = useRef<HTMLDialogElement>(null)
  const titleId = useId()
  useEffect(() => {
    const element = dialog.current
    element?.showModal()
    return () => element?.close()
  }, [])
  const field = (key: keyof DatasourceInput, value: string | number) => setForm(current => ({ ...current, [key]: value }))
  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (saving) return
    setSaving(true)
    setError('')
    try { await save(form) }
    catch (failure) { reportClientError('datasource_save', failure, 'SourceDialog'); setError(failureText(t, 'failedSave', failure)) }
    finally { setSaving(false) }
  }
  return <dialog ref={dialog} className="hgm-dialog" aria-labelledby={titleId} onCancel={event => { event.preventDefault(); if (!saving && form === initial) close() }}><form onSubmit={event => void submit(event)}>
    <header><h2 id={titleId}>{source ? t('editSource') : t('addSource')}</h2><button type="button" aria-label={t('cancel')} disabled={saving} onClick={close}>×</button></header>
    <div className="hgm-form-grid">
      <Field label={t('name')}><input required maxLength={64} value={form.displayName} onChange={event => field('displayName', event.target.value)} /></Field>
      <Field label={t('host')}><input required value={form.host} onChange={event => field('host', event.target.value)} /></Field>
      <Field label={t('port')}><input required type="number" min={1} max={65535} value={form.port} onChange={event => field('port', Number(event.target.value))} /></Field>
      <Field label={t('database')}><input required value={form.database} onChange={event => field('database', event.target.value)} /></Field>
      <Field label={t('user')}><input required value={form.user} onChange={event => field('user', event.target.value)} /></Field>
      <Field label={source ? t('optionalPassword') : t('password')}><input required={!source} type="password" autoComplete="new-password" value={form.password} onChange={event => field('password', event.target.value)} /></Field>
      <Field label={t('sslMode')}><select value={form.sslMode} onChange={event => field('sslMode', event.target.value)}>{sslModes.map(mode => <option key={mode}>{mode}</option>)}</select></Field>
      <Field label={t('connectTimeout')}><input required type="number" min={1} max={30} value={form.connectTimeoutSeconds} onChange={event => field('connectTimeoutSeconds', Number(event.target.value))} /></Field>
      <Field label={t('queryTimeout')}><input required type="number" min={1} max={30} value={form.queryTimeoutSeconds} onChange={event => field('queryTimeoutSeconds', Number(event.target.value))} /></Field>
    </div>
    {error && <p className="hgm-form-error" role="alert">{error}</p>}
    <footer><button type="button" disabled={saving} onClick={close}>{t('cancel')}</button><button className="hgm-primary" disabled={saving}>{t('save')}</button></footer>
  </form></dialog>
}

function Field({ label, children }: { label: string; children: ReactNode }) { return <label className="hgm-field"><span>{label}</span>{children}</label> }
