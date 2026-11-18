import { useState, type ReactNode } from 'react'
import type { Progress } from './api'
import { bytes, rate, stateLabel } from './format'

export function Poster({ src, title, size = 'md' }: { src: string; title: string; size?: 'xs' | 'sm' | 'md' | 'lg' }) {
  const [failed, setFailed] = useState(false)
  if (!src || failed) {
    return (
      <div className={`poster poster-${size} poster-empty`} aria-hidden="true">
        <span>{title.slice(0, 1).toUpperCase()}</span>
      </div>
    )
  }
  return (
    <img
      className={`poster poster-${size}`}
      src={src}
      alt=""
      loading="lazy"
      onError={() => setFailed(true)}
    />
  )
}

export function StateBadge({ state, label }: { state: string; label?: string }) {
  return (
    <span className={`badge badge-${state}`}>
      <span className="badge-dot" aria-hidden="true" />
      {label ?? stateLabel(state)}
    </span>
  )
}

export function ProgressBar({ percent }: { percent: number }) {
  return (
    <div className="bar" role="progressbar" aria-valuenow={Math.round(percent)} aria-valuemin={0} aria-valuemax={100}>
      <div style={{ width: `${percent}%` }} />
    </div>
  )
}

/** One line describing a download's progress. */
export function progressText(p: Progress): string {
  if (!p.hasInfo) return `Fetching metadata · ${p.peers} peers`
  return [`${p.percent.toFixed(1)}%`, `${bytes(p.done)} of ${bytes(p.total)}`, rate(p.rate), `${p.peers} peers`]
    .filter(Boolean)
    .join(' · ')
}

export function Notice({ kind = 'info', children }: { kind?: 'info' | 'warn' | 'error'; children: ReactNode }) {
  return <div className={`notice notice-${kind}`}>{children}</div>
}

export function Spinner({ label }: { label: string }) {
  return (
    <div className="spinner-row">
      <span className="spinner" aria-hidden="true" />
      {label}
    </div>
  )
}
