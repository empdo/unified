import type { ReactNode } from 'react'
import type { Item, Progress } from './api'
import { ProgressBar, StateBadge } from './components'
import { airDate, rate, timeLeft } from './format'

const time = (iso: string) => new Date(iso).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })

/**
 * An episode's or movie's state, with what is happening to it: the search
 * for a release while it is wanted, and its download's progress after.
 * Pass the download to show its progress.
 */
export function ItemStatus({ item, download: p }: { item: Item; download?: Progress }) {
  if (item.state === 'wanted') return <WantedStatus item={item} />
  if (item.state !== 'downloading' || !p) return <StateBadge state={item.state} />
  if (!p.hasInfo) {
    return (
      <Status title={p.name} badge={<StateBadge state="searching" label="Finding peers…" />}>
        Getting the torrent from {p.peers > 0 ? `${p.peers} peers` : 'peers'}
      </Status>
    )
  }
  if (p.done === 0 && p.peers === 0) {
    return (
      <Status title={p.name} badge={<StateBadge state="searching" label="Waiting for peers" />}>
        No one is sharing it right now
      </Status>
    )
  }
  const detail = [rate(p.rate), timeLeft(p.done, p.total, p.rate)].filter(Boolean).join(' · ')
  return (
    <Status title={p.name} badge={<StateBadge state="downloading" label={`Downloading ${Math.floor(p.percent)}%`} />}>
      <ProgressBar percent={p.percent} />
      {detail}
    </Status>
  )
}

function WantedStatus({ item }: { item: Item }) {
  const s = item.search
  const today = new Date().toISOString().slice(0, 10)
  if (item.airDate > today && (item.season > 0 || item.episode > 0)) {
    return <Status badge={<StateBadge state="wanted" />}>Airs {airDate(item.airDate)}</Status>
  }
  if (s?.state === 'searching') {
    return <Status badge={<StateBadge state="searching" label="Searching…" />}>Looking on the indexers</Status>
  }
  if (s?.state === 'notFound') {
    return (
      <Status title={s.note} badge={<StateBadge state="wanted" />}>
        {s.note ?? 'Nothing found'} · {time(s.at)}
      </Status>
    )
  }
  return <Status badge={<StateBadge state="wanted" label="Waiting to search" />} />
}

function Status(props: { badge: ReactNode; title?: string; children?: ReactNode }) {
  return (
    <span className="item-status" title={props.title}>
      {props.badge}
      {props.children && <span className="small muted item-status-detail">{props.children}</span>}
    </span>
  )
}
