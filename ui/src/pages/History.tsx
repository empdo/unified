import { useEffect, useState } from 'react'
import { api, type HistoryEvent, type HistoryEventKind } from '../api'
import { Notice, Spinner, StateBadge } from '../components'
import { useAction } from '../hooks'
import { href } from '../router'

const FILTERS: { key: string; label: string; events?: HistoryEventKind[] }[] = [
  { key: 'all', label: 'Everything' },
  { key: 'downloads', label: 'Downloads', events: ['grabbed', 'imported', 'failed', 'cancelled', 'seeded'] },
  { key: 'failed', label: 'Failed', events: ['failed'] },
  { key: 'library', label: 'Library', events: ['added', 'scanned', 'deleted', 'removed'] },
]

const LABELS: Record<HistoryEventKind, string> = {
  added: 'Added',
  scanned: 'Found on disk',
  grabbed: 'Grabbed',
  imported: 'Imported',
  failed: 'Failed',
  cancelled: 'Cancelled',
  deleted: 'Files deleted',
  removed: 'Removed',
  seeded: 'Seeded',
}

/** Which badge colours an event takes. */
const BADGES: Record<HistoryEventKind, string> = {
  added: 'wanted',
  scanned: 'imported',
  grabbed: 'downloading',
  imported: 'imported',
  failed: 'failed',
  cancelled: 'queued',
  deleted: 'queued',
  removed: 'queued',
  seeded: 'done',
}

export default function HistoryPage({ filter, media }: { filter: string; media?: number }) {
  const current = FILTERS.find((f) => f.key === filter) ?? FILTERS[0]
  return (
    <>
      <div className="page-head">
        <h1>History</h1>
      </div>
      {media && (
        <p className="small muted">
          Of one movie or show only. <a href={href('/history')}>Show everything</a>
        </p>
      )}
      <div className="pills" role="tablist" aria-label="Show">
        {FILTERS.map((f) => (
          <a
            key={f.key}
            role="tab"
            className="pill"
            aria-selected={f === current}
            href={href(`/history?show=${f.key}${media ? `&media=${media}` : ''}`)}
          >
            {f.label}
          </a>
        ))}
      </div>
      <Events key={current.key} media={media} events={current.events?.join(',')} />
    </>
  )
}

/** The history of one movie or show, for its page. */
export function MediaHistory({ media }: { media: number }) {
  return (
    <section className="media-history">
      <div className="section-head">
        <h2>History</h2>
      </div>
      <Events media={media} limit={10} compact />
    </section>
  )
}

/** Events, newest first, grouped by day, with a button to load older ones. */
function Events(props: { media?: number; events?: string; limit?: number; compact?: boolean }) {
  const { media, events, limit = 100, compact } = props
  const [list, setList] = useState<HistoryEvent[]>()
  const [more, setMore] = useState(false)
  const load = useAction()

  const fetchPage = (before?: number) =>
    load.run(async () => {
      const page = await api.history({ media, events, before, limit })
      setList((prev) => (before ? [...(prev ?? []), ...page.events] : page.events))
      setMore(page.more)
    })

  useEffect(() => {
    fetchPage()
  }, [media, events])

  if (!list) return load.error ? <Notice kind="error">{load.error}</Notice> : <Spinner label="Loading history…" />
  if (list.length === 0) return <p className="muted">Nothing has happened yet.</p>

  const days: { day: string; events: HistoryEvent[] }[] = []
  for (const e of list) {
    const day = dayLabel(e.at)
    if (days.at(-1)?.day !== day) days.push({ day, events: [] })
    days.at(-1)!.events.push(e)
  }

  return (
    <div className="history">
      {days.map((d) => (
        <section key={d.day} className="history-day">
          <h3 className="history-date">{d.day}</h3>
          <ul className="list">
            {d.events.map((e) => (
              <li key={e.id} className="history-event">
                <span className="history-time small muted">{time(e.at)}</span>
                <StateBadge state={BADGES[e.event]} label={LABELS[e.event] ?? e.event} />
                <span className="history-what">
                  {compact || !e.inLibrary || !e.mediaId ? (
                    <span className="history-title">{e.title || '—'}</span>
                  ) : (
                    <a className="history-title" href={href(`/media/${e.mediaId}`)}>
                      {e.title}
                    </a>
                  )}
                  {e.detail && <span className="small muted history-detail">{e.detail}</span>}
                </span>
              </li>
            ))}
          </ul>
        </section>
      ))}
      {load.error && <Notice kind="error">{load.error}</Notice>}
      {more && (
        <div>
          {compact && media ? (
            <a className="button small" href={href(`/history?media=${media}`)}>
              All of its history
            </a>
          ) : (
            <button className="button" disabled={load.busy} onClick={() => fetchPage(list.at(-1)?.id)}>
              {load.busy ? 'Loading…' : 'Show older'}
            </button>
          )}
        </div>
      )}
    </div>
  )
}

function dayLabel(at: string) {
  const d = new Date(at)
  const today = new Date()
  const yesterday = new Date(today.getTime() - 86400000)
  if (d.toDateString() === today.toDateString()) return 'Today'
  if (d.toDateString() === yesterday.toDateString()) return 'Yesterday'
  return d.toLocaleDateString('en-GB', {
    weekday: 'long',
    day: 'numeric',
    month: 'long',
    year: d.getFullYear() === today.getFullYear() ? undefined : 'numeric',
  })
}

const time = (at: string) => new Date(at).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })
