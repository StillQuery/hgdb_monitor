import { useId, useState, type MouseEvent } from 'react'
import type { Snapshot } from './types.js'

interface Series { label: string; color: string; values: number[] }

function timeLabel(value: string | undefined): string {
  if (value === undefined) return '—'
  return Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(value))
}

export function TrendChart({ points, series, format = compact }: { points: Snapshot[]; series: Series[]; format?: (value: number) => string }) {
  const [hovered, setHovered] = useState<number>()
  const gradientId = useId().replaceAll(':', '')
  const all = series.flatMap(item => item.values).filter(Number.isFinite)
  if (points.length < 2 || all.length === 0) return <div className="hgm-empty-chart">—</div>
  const max = Math.max(...all, 1)
  const min = Math.min(...all, 0)
  const range = Math.max(max - min, 1)
  const xAt = (index: number) => index * 100 / Math.max(1, points.length - 1)
  const yAt = (value: number) => 92 - ((value - min) / range) * 78
  const path = (values: number[]) => {
    let drawing = false
    return values.map((value, index) => {
      if (!Number.isFinite(value)) { drawing = false; return '' }
      const command = drawing ? 'L' : 'M'
      drawing = true
      return `${command}${xAt(index).toFixed(2)},${yAt(value).toFixed(2)}`
    }).join(' ')
  }
  const move = (event: MouseEvent<SVGSVGElement>) => {
    const bounds = event.currentTarget.getBoundingClientRect()
    const ratio = Math.min(1, Math.max(0, (event.clientX - bounds.left) / Math.max(1, bounds.width)))
    setHovered(Math.round(ratio * (points.length - 1)))
  }
  const middle = Math.floor((points.length - 1) / 2)
  const hoverX = hovered === undefined ? undefined : xAt(hovered)
  const firstSeries = series[0]
  const areaPath = firstSeries !== undefined && firstSeries.values.every(Number.isFinite) ? `${path(firstSeries.values)} L100,92 L0,92 Z` : undefined

  return <div className="hgm-chart-wrap">
    <div className="hgm-chart-body">
      <div className="hgm-y-axis"><span>{format(max)}</span><span>{format(min + range / 2)}</span><span>{format(min)}</span></div>
      <div className="hgm-chart-canvas">
        <svg className="hgm-chart" viewBox="0 0 100 100" preserveAspectRatio="none" aria-hidden onMouseMove={move} onMouseLeave={() => setHovered(undefined)}>
          <defs><linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1"><stop offset="0" stopColor={firstSeries?.color ?? 'var(--hgm-blue)'} stopOpacity=".2" /><stop offset="1" stopColor={firstSeries?.color ?? 'var(--hgm-blue)'} stopOpacity="0" /></linearGradient></defs>
          <path className="hgm-chart-grid" d="M0 14H100M0 53H100M0 92H100" />
          {areaPath !== undefined && <path className="hgm-chart-area" d={areaPath} fill={`url(#${gradientId})`} />}
          {series.map(item => <path key={item.label} d={path(item.values)} fill="none" stroke={item.color} strokeWidth="1.8" vectorEffect="non-scaling-stroke" />)}
          {hoverX !== undefined && <><path className="hgm-chart-cursor" d={`M${hoverX},10V94`} />{series.map(item => { const value = item.values[hovered!]; return value === undefined || !Number.isFinite(value) ? null : <circle key={item.label} cx={hoverX} cy={yAt(value)} r="2.5" fill={item.color} vectorEffect="non-scaling-stroke" /> })}</>}
        </svg>
        {hovered !== undefined && <div className="hgm-chart-tooltip" style={{ left: `${Math.min(88, Math.max(12, hoverX ?? 50))}%` }}>
          <b>{timeLabel(points[hovered]?.collectedAt)}</b>
          {series.map(item => { const value = item.values[hovered]; return <span key={item.label}><i style={{ background: item.color }} />{item.label}<strong>{value === undefined || !Number.isFinite(value) ? '—' : format(value)}</strong></span> })}
        </div>}
        <div className="hgm-x-axis"><span>{timeLabel(points[0]?.collectedAt)}</span><span>{timeLabel(points[middle]?.collectedAt)}</span><span>{timeLabel(points.at(-1)?.collectedAt)}</span></div>
      </div>
    </div>
    <div className="hgm-legend">{series.map(item => { const latest = item.values.at(-1); return <span key={item.label}><i style={{ background: item.color }} />{item.label}<b>{latest === undefined || !Number.isFinite(latest) ? '—' : format(latest)}</b></span> })}</div>
  </div>
}

export function compact(value: number): string {
  return Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 }).format(value)
}

export function rollingRate(points: Snapshot[], key: keyof Snapshot, windowSeconds = 60): number[] {
  return points.map((point, index) => {
    if (index === 0) return 0
    const currentTime = Date.parse(point.collectedAt)
    let start = index
    while (start > 0 && currentTime - Date.parse(points[start - 1]!.collectedAt) <= windowSeconds * 1000) start -= 1
    if (start === index) start = index - 1
    let increase = 0
    for (let cursor = start + 1; cursor <= index; cursor += 1) {
      const previous = Number(points[cursor - 1]![key])
      const current = Number(points[cursor]![key])
      increase += current >= previous ? current - previous : current
    }
    const elapsed = (currentTime - Date.parse(points[start]!.collectedAt)) / 1000
    return elapsed > 0 ? increase / elapsed : 0
  })
}

export function cacheHit(points: Snapshot[], windowSeconds = 60): number[] {
  return points.map((point, index) => {
    if (index === 0) return Number.NaN
    const currentTime = Date.parse(point.collectedAt)
    let start = index
    while (start > 0 && currentTime - Date.parse(points[start - 1]!.collectedAt) <= windowSeconds * 1000) start -= 1
    if (start === index) start = index - 1
    let hit = 0; let read = 0
    for (let cursor = start + 1; cursor <= index; cursor += 1) {
      const previous = points[cursor - 1]!; const current = points[cursor]!
      hit += current.blocksHit >= previous.blocksHit ? current.blocksHit - previous.blocksHit : current.blocksHit
      read += current.blocksRead >= previous.blocksRead ? current.blocksRead - previous.blocksRead : current.blocksRead
    }
    return hit + read === 0 ? Number.NaN : hit * 100 / (hit + read)
  })
}
