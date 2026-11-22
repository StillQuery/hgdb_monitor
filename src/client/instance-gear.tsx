import { useId, useRef, useState, type CSSProperties, type PointerEvent } from 'react'

const toothCount = 8
const toothPitch = 360 / toothCount
const toothProfile = [[-toothPitch / 2, 37], [-16, 37], [-14.3, 38], [-12.5, 42], [-8.5, 48], [-7, 49], [7, 49], [8.5, 48], [12.5, 42], [14.3, 38], [16, 37]] as const
const outline = `M${Array.from({ length: toothCount }, (_, tooth) => toothProfile.map(([offset, radius]) => {
  const radians = (tooth * toothPitch + offset) * Math.PI / 180
  return `${(radius * Math.cos(radians)).toFixed(2)},${(radius * Math.sin(radians)).toFixed(2)}`
})).flat().join('L')}Z`

interface GearProps {
  count: number
  slot: number
  label: string
  hint: string
  onSelect(slot: number): void
}

export function InstanceGear({ count, slot, label, hint, onSelect }: GearProps) {
  const id = useId()
  const profileId = `${id}-profile`
  const finishId = `${id}-finish`
  const edgeId = `${id}-edge`
  const cropId = `${id}-crop`
  const drag = useRef<{ pointerId: number; start: number; delta: number }>()
  const suppressClick = useRef(false)
  const [offset, setOffset] = useState(0)
  const [dragging, setDragging] = useState(false)
  const [rotation, setRotation] = useState<number>()

  function move(direction: number) {
    if (count < 2) return
    setRotation(value => (value ?? 0) + direction * toothPitch)
    onSelect((slot + direction + count) % count)
  }

  function finish(event: PointerEvent<HTMLButtonElement>) {
    if (drag.current?.pointerId !== event.pointerId) return
    const { delta } = drag.current
    drag.current = undefined
    setDragging(false)
    setOffset(0)
    if (event.type === 'pointerup' && Math.abs(delta) >= 18) move(Math.sign(delta))
  }

  return <span className="hgm-gear-slot">
    <button className="hgm-gear" type="button" disabled={count < 2} aria-label={label} title={count > 1 ? hint : undefined}
      onPointerDown={event => {
        if (!event.isPrimary || event.button !== 0 || drag.current) return
        suppressClick.current = false
        drag.current = { pointerId: event.pointerId, start: event.clientY, delta: 0 }
        event.currentTarget.setPointerCapture(event.pointerId)
        setDragging(true)
      }}
      onPointerMove={event => {
        const gesture = drag.current
        if (gesture?.pointerId !== event.pointerId) return
        gesture.delta = event.clientY - gesture.start
        if (Math.abs(gesture.delta) > 5) suppressClick.current = true
        if (suppressClick.current) setOffset(Math.max(-toothPitch, Math.min(toothPitch, gesture.delta * 1.15)))
      }}
      onPointerUp={finish} onPointerCancel={finish} onLostPointerCapture={finish}
      onClick={event => {
        if (event.detail && suppressClick.current) { suppressClick.current = false; return }
        const bounds = event.currentTarget.getBoundingClientRect()
        move(event.detail && event.clientY < bounds.top + bounds.height / 2 ? -1 : 1)
      }}
      onKeyDown={event => {
        const direction = event.key === 'ArrowUp' ? -1 : event.key === 'ArrowDown' ? 1 : 0
        if (!direction) return
        event.preventDefault()
        if (!event.repeat && !drag.current) move(direction)
      }}>
      <svg viewBox="0 0 66 104" aria-hidden="true">
        <defs>
          <path id={profileId} d={outline} />
          <clipPath id={cropId}><rect y="9" width="56" height="86" /></clipPath>
          <linearGradient id={finishId} x1="0%" y1="0%" x2="81%" y2="100%">
            <stop stopColor="var(--hgm-gear-steel-mid)" /><stop offset=".38" stopColor="var(--hgm-gear-steel-light)" />
            <stop offset=".55" stopColor="var(--hgm-gear-steel-mid)" /><stop offset="1" stopColor="var(--hgm-gear-steel-dark)" />
          </linearGradient>
          <linearGradient id={edgeId} x1="26%" y1="0%" x2="72%" y2="100%">
            <stop stopColor="var(--hgm-gear-edge-light)" /><stop offset="1" stopColor="var(--hgm-gear-steel-mid)" />
          </linearGradient>
        </defs>
        <g clipPath={`url(#${cropId})`}>
          <g className="hgm-gear-rotor" data-dragging={dragging} style={{ '--hgm-gear-angle': `${(rotation ?? 0) + offset}deg` } as CSSProperties}>
            <g transform="translate(-13 52)">
              <use href={`#${profileId}`} className="hgm-gear-depth" transform="translate(2.5 3)" />
              <use href={`#${profileId}`} fill={`url(#${finishId})`} stroke={`url(#${edgeId})`} strokeWidth="1.35" strokeLinejoin="round" />
              <circle className="hgm-gear-hub-rim" r="23.5" />
              <circle className="hgm-gear-hub" r="19.5" />
            </g>
          </g>
        </g>
        <path className="hgm-gear-pointer" data-engaged={rotation !== undefined} key={rotation ?? 'initial'} d="M43 52h8" />
      </svg>
    </button>
  </span>
}
