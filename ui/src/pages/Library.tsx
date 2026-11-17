import { useState } from 'react'
import { api, type MediaSummary, type Progress } from '../api'
import { Notice, Poster, ProgressBar, Spinner } from '../components'
import { useData } from '../hooks'
import { href } from '../router'
import { useActiveByMedia, useStatus } from '../status'

type Filter = 'all' | 'movie' | 'show'

export default function Library() {
  const library = useData(api.library, [], 10000)
  const status = useStatus()
  const active = useActiveByMedia()
  const [filter, setFilter] = useState<Filter>('all')

  const items = (library.data ?? []).filter((m) => filter === 'all' || m.kind === filter)
  return (
    <>
      {status && !status.tmdb && (
        <Notice kind="warn">
          Add a TMDB API key in <a href={href('/settings')}>Settings</a> to search for movies and shows.
        </Notice>
      )}
      {status && status.indexers === 0 && (
        <Notice kind="warn">
          Add an indexer in <a href={href('/settings')}>Settings</a>, or nothing can be downloaded.
        </Notice>
      )}
      <div className="page-head">
        <h1>Library</h1>
        <div className="segmented" role="radiogroup" aria-label="Show">
          {(['all', 'movie', 'show'] as const).map((f) => (
            <button key={f} role="radio" aria-checked={filter === f} onClick={() => setFilter(f)}>
              {{ all: 'All', movie: 'Movies', show: 'Shows' }[f]}
            </button>
          ))}
        </div>
      </div>
      {library.error && <Notice kind="error">{library.error}</Notice>}
      {library.loading && !library.data && <Spinner label="Loading library…" />}
      {library.data && items.length === 0 && (
        <div className="empty">
          <h2>{library.data.length === 0 ? 'Your library is empty' : 'Nothing here'}</h2>
          <p className="muted">Search for a movie or show in the bar at the top to add it.</p>
        </div>
      )}
      <div className="poster-grid">
        {items.map((m) => (
          <Card key={m.id} media={m} downloads={active.get(m.id) ?? []} />
        ))}
      </div>
    </>
  )
}

function Card({ media, downloads }: { media: MediaSummary; downloads: Progress[] }) {
  const c = media.counts
  const total = (c.ignored ?? 0) + (c.wanted ?? 0) + (c.downloading ?? 0) + (c.imported ?? 0)
  let label: string
  let tone: string
  if (downloads.length > 0) {
    const p = downloads[0]
    label = p.hasInfo ? `Downloading ${Math.floor(p.percent)}%` : 'Starting download…'
    tone = 'downloading'
  } else if (media.kind === 'movie') {
    tone = c.imported ? 'imported' : c.wanted ? 'wanted' : c.downloading ? 'downloading' : 'ignored'
    label = { imported: 'In library', wanted: 'Wanted', downloading: 'Downloading', ignored: 'Not wanted' }[tone]!
  } else if (total > 0 && c.imported === total) {
    tone = 'imported'
    label = 'Complete'
  } else {
    tone = c.wanted ? 'wanted' : c.imported ? 'imported' : 'ignored'
    label = c.wanted ? `${c.wanted} wanted · ${c.imported ?? 0}/${total}` : `${c.imported ?? 0} of ${total} episodes`
  }
  return (
    <a className="card" href={href(`/media/${media.id}`)}>
      <div className="card-art">
        <Poster src={media.poster} title={media.title} />
        {downloads.length > 0 && downloads[0].hasInfo && <ProgressBar percent={downloads[0].percent} />}
      </div>
      <div className="card-title" title={media.title}>
        {media.title}
      </div>
      <div className="card-meta">
        {media.year > 0 && `${media.year} · `}
        {media.kind === 'movie' ? 'Movie' : 'Show'}
      </div>
      <div className="card-status">
        <span className={`dot-${tone}`} />
        <span>{label}</span>
      </div>
    </a>
  )
}
