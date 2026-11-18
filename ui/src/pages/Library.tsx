import { api, type MediaSummary, type Progress } from '../api'
import { Notice, Poster, ProgressBar, Spinner, StateBadge } from '../components'
import { DiskWarning } from '../disk'
import { plural, rate, timeLeft } from '../format'
import { useAction, useData } from '../hooks'
import { href } from '../router'
import { useActiveByMedia, useStatus } from '../status'

export default function Library() {
  const library = useData(api.library, [], 10000)
  const status = useStatus()
  const active = useActiveByMedia()

  const all = library.data ?? []
  const shows = all.filter((m) => m.kind === 'show')
  const movies = all.filter((m) => m.kind === 'movie')

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
      <DiskWarning />
      <Tonight titles={new Map(all.map((m) => [m.id, m.title]))} />
      {library.error && <Notice kind="error">{library.error}</Notice>}
      {library.loading && !library.data && <Spinner label="Loading library…" />}
      {library.data?.length === 0 && (
        <div className="empty">
          <h2>Your library is empty</h2>
          <p className="muted">Search for a movie or show at the top of the page to add it.</p>
        </div>
      )}
      {all.length > 0 && (
        <div className="listings">
          <section className="listings-shows">
            <div className="section-head">
              <h2>Shows</h2>
              <span className="muted small">{plural(shows.length, 'show')}</span>
            </div>
            {shows.length === 0 && <p className="muted">No shows yet.</p>}
            <div className="show-grid">
              {shows.map((m) => (
                <ShowCard key={m.id} media={m} downloads={active.get(m.id) ?? []} />
              ))}
            </div>
          </section>

          <section className="listings-movies">
            <div className="section-head">
              <h2>Movies</h2>
              <span className="muted small">{plural(movies.length, 'movie')}</span>
            </div>
            {movies.length === 0 && <p className="muted">No movies yet.</p>}
            {movies.map((m) => (
              <MovieCard key={m.id} media={m} downloads={active.get(m.id) ?? []} reload={library.reload} />
            ))}
          </section>
        </div>
      )}
    </>
  )
}

/** The strip of running downloads at the top of the library. */
function Tonight({ titles }: { titles: Map<number, string> }) {
  const status = useStatus()
  const action = useAction()
  if (!status) return null
  const active = status.status.active
  const search = status.status.search
  const shown = active.slice(0, search.current ? 2 : 3)
  const waiting = search.queued > 0 ? `${search.queued} more waiting` : ''

  return (
    <section className="tonight" aria-label="Downloading now">
      <div className="tonight-label">
        <span className="tonight-title">Tonight</span>
        <span className="small">
          {active.length > 0
            ? `${plural(active.length, 'download')} running`
            : search.current
              ? 'Looking for releases'
              : 'Nothing downloading'}
        </span>
      </div>
      <div className="tonight-items">
        {search.current && (
          <a className="tonight-item" href={href(`/media/${search.current.mediaId}`)}>
            <span className="tonight-name">
              <span>{search.current.label}</span>
            </span>
            <div className="bar bar-indeterminate" aria-hidden="true">
              <div />
            </div>
            <span className="tonight-meta">{['Searching the indexers…', waiting].filter(Boolean).join(' · ')}</span>
          </a>
        )}
        {shown.map((p) => (
          <a key={p.id} className="tonight-item" href={href(`/media/${p.mediaId}`)}>
            <span className="tonight-name">
              <span>{titles.get(p.mediaId) ?? p.name}</span>
              {p.hasInfo && <strong>{Math.floor(p.percent)}%</strong>}
            </span>
            <ProgressBar percent={p.hasInfo ? p.percent : 0} />
            <span className="tonight-meta">{tonightMeta(p)}</span>
          </a>
        ))}
        {active.length > shown.length && (
          <a className="tonight-more" href={href('/downloads')}>
            +{active.length - shown.length} more
          </a>
        )}
      </div>
      <div className="tonight-side">
        <button className="button" disabled={action.busy} onClick={() => action.run(api.searchNow)}>
          {action.busy ? 'Searching…' : 'Search for wanted now'}
        </button>
        <span className="small">{action.error ?? 'Also runs by itself every 30 minutes'}</span>
      </div>
    </section>
  )
}

function tonightMeta(p: Progress): string {
  if (!p.hasInfo) return `Starting · ${plural(p.peers, 'peer')}`
  return [rate(p.rate), plural(p.peers, 'peer'), timeLeft(p.done, p.total, p.rate)].filter(Boolean).join(' · ')
}

/** A show in the library; its episodes are managed on the show page. */
function ShowCard({ media, downloads }: { media: MediaSummary; downloads: Progress[] }) {
  const c = media.counts
  const total = (c.ignored ?? 0) + (c.wanted ?? 0) + (c.downloading ?? 0) + (c.imported ?? 0)
  const imported = c.imported ?? 0
  const summary =
    total > 0 && imported === total
      ? `All ${plural(total, 'episode')} in library`
      : [`${imported} of ${plural(total, 'episode')} in library`, c.wanted && `${c.wanted} wanted`]
          .filter(Boolean)
          .join(' · ')
  const p = downloads[0]

  return (
    <a className="movie show-card" href={href(`/media/${media.id}`)}>
      <Poster src={media.poster} title={media.title} size="sm" />
      <span className="movie-body">
        <span className="movie-title">
          {media.title} {media.year > 0 && <span className="muted">({media.year})</span>}
        </span>
        <span className="muted small">{summary}</span>
        {downloads.length > 0 || c.downloading ? (
          <StateBadge
            state="downloading"
            label={p?.hasInfo ? `Downloading ${Math.floor(p.percent)}%` : `${c.downloading ?? 1} downloading`}
          />
        ) : null}
      </span>
    </a>
  )
}

function MovieCard({ media, downloads, reload }: { media: MediaSummary; downloads: Progress[]; reload: () => void }) {
  const action = useAction()
  const c = media.counts
  const state = downloads.length > 0 || c.downloading ? 'downloading' : c.imported ? 'imported' : c.wanted ? 'wanted' : 'ignored'
  const p = downloads[0]
  const label = state === 'downloading' && p?.hasInfo ? `Downloading ${Math.floor(p.percent)}%` : undefined

  // The library lists counts only, so look up the movie's item before changing it.
  const want = (want: boolean) =>
    action
      .run(async () => {
        const d = await api.media(media.id)
        if (d.movie) await api.wantItem(d.movie.id, want)
      })
      .then(reload)

  return (
    <article className="movie">
      <a href={href(`/media/${media.id}`)} tabIndex={-1} aria-hidden="true">
        <Poster src={media.poster} title={media.title} size="sm" />
      </a>
      <div className="movie-body">
        <a className="movie-title" href={href(`/media/${media.id}`)}>
          {media.title} {media.year > 0 && <span className="muted">({media.year})</span>}
        </a>
        <StateBadge state={state} label={label} />
        {action.error && <span className="error-text">{action.error}</span>}
      </div>
      {state === 'ignored' && (
        <button className="button small" disabled={action.busy} onClick={() => want(true)}>
          Download
        </button>
      )}
      {state === 'wanted' && (
        <button className="button small" disabled={action.busy} onClick={() => want(false)}>
          Stop wanting
        </button>
      )}
      {(state === 'imported' || state === 'downloading') && (
        <a className="button small" href={href(`/releases?media=${media.id}`)}>
          Releases
        </a>
      )}
    </article>
  )
}
