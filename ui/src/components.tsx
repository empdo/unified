import { useState, type ReactNode } from 'react'
import type { Progress } from './api'
import { bytes, plural, rate, stateLabel } from './format'

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
  return [`${p.percent.toFixed(1)}%`, `${bytes(p.done)} of ${bytes(p.total)}`, rate(p.rate), peersText(p)]
    .filter(Boolean)
    .join(' · ')
}

/** Peers, how they are connected, and how many are seeders: "12 peers (9 uTP, 3 TCP), 2 seeders". */
export function peersText(p: Progress): string {
  const transports = p.utp + p.tcp > 0 ? ` (${p.utp} uTP, ${p.tcp} TCP)` : ''
  return `${p.peers} peers${transports}, ${p.seeders} ${p.seeders === 1 ? 'seeder' : 'seeders'}`
}

/**
 * Why a download that has its metadata has nothing yet, judging by who it is
 * connected to: whether it is the swarm, or peers that won't send.
 */
export function stuckReason(p: Progress): string | undefined {
  if (!p.hasInfo || p.done > 0) return undefined
  const minutes = p.started ? (Date.now() - Date.parse(p.started)) / 60000 : 0
  if (minutes < 1) return undefined
  const reason = swarmReason(p, minutes)
  if (!p.givesUpAt) return reason
  const at = new Date(p.givesUpAt).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })
  return `${reason} Another release is tried at ${at} unless it starts.`
}

function swarmReason(p: Progress, minutes: number): string {
  if (p.peers === 0) {
    return p.known === 0
      ? 'No peers found: nobody seems to be sharing this release right now.'
      : `Knows of ${p.known} peers but can't connect to any of them.`
  }
  if (p.best === 0) {
    return `None of the ${p.peers} connected peers has any of it yet, so there is no seeder to download from.`
  }
  const who = p.seeders > 0 ? plural(p.seeders, 'seeder') : `peers with up to ${Math.round(p.best * 100)}% of it`
  return `Connected to ${who}, but nothing has arrived in ${Math.round(minutes)} minutes: they aren't letting us download (choking). See Peer details.`
}

export function Notice({ kind = 'info', children }: { kind?: 'info' | 'warn' | 'error'; children: ReactNode }) {
  return <div className={`notice notice-${kind}`}>{children}</div>
}

/** A button's label, or while its action runs, a spinner and busyLabel. */
export function Busy({ on, label, children }: { on: boolean; label?: string; children: ReactNode }) {
  if (!on) return <>{children}</>
  return (
    <>
      <span className="spinner spinner-inline" aria-hidden="true" />
      {label ?? children}
    </>
  )
}

export function Spinner({ label }: { label: string }) {
  return (
    <div className="spinner-row">
      <span className="spinner" aria-hidden="true" />
      {label}
    </div>
  )
}
