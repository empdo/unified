import { api, type FrontStory, type MediaSummary, type Progress } from '../api'
import { Busy, Notice, Poster, ProgressBar, Spinner, StateBadge } from '../components'
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
      <FrontPage />
      {library.error && <Notice kind="error">{library.error}</Notice>}
      {library.loading && !library.data && <Spinner label="Loading library…" />}
      {library.data?.length === 0 && (
        <div className="empty">
          <h2>Your library is empty</h2>
          <p className="muted">
            Search for a movie or show at the top of the page to add it, <a href={href('/discover')}>browse what's
            popular</a>, or <a href={href('/scan')}>find what's already in your movie and TV folders</a>.
          </p>
        </div>
      )}
      {all.length > 0 && (
        <div className="listings">
          <section className="listings-shows">
            <div className="section-head">
              <h2>Shows</h2>
              <span className="muted small">{plural(shows.length, 'show')}</span>
              <a className="small section-link" href={href('/scan')}>
                Find files already on disk
              </a>
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

/** The movies and shows that arrived last, laid out as a front page: a lead story and up to three more. */
function FrontPage() {
  const stories = useData(api.frontPage, [], 30000).data ?? []
  if (stories.length === 0) return null
  const [lead, ...rest] = stories
  return (
    <section className="front" aria-label="Latest arrivals">
      <article className="lead">
        <a href={href(`/media/${lead.id}`)} tabIndex={-1} aria-hidden="true">
          <Poster src={lead.poster} title={lead.title} size="lead" />
        </a>
        <div className="lead-body">
          <span className="eyebrow">{isFresh(lead.at) ? 'Just in' : 'Latest arrival'}</span>
          <h2 className="lead-headline">
            <a href={href(`/media/${lead.id}`)}>{lead.title}</a>
          </h2>
          <p className="kicker">{kicker(lead)}</p>
          {lead.overview && <p className="overview lead-overview">{lead.overview}</p>}
          {lead.kind === 'show' && lead.arrived.length > 1 && (
            <ul className="arrived">
              {lead.arrived.slice(0, 5).map((a) => (
                <li key={a}>{episodeName(lead, a)}</li>
              ))}
              {lead.arrived.length > 5 && <li className="muted">and {lead.arrived.length - 5} more</li>}
            </ul>
          )}
        </div>
      </article>
      {rest.length > 0 && (
        <div className="briefs">
          {rest.map((s) => (
            <a key={s.id} className="brief" href={href(`/media/${s.id}`)}>
              <Poster src={s.poster} title={s.title} size="sm" />
              <span className="brief-body">
                <span className="brief-headline">{s.title}</span>
                <span className="kicker">{kicker(s)}</span>
              </span>
            </a>
          ))}
        </div>
      )}
    </section>
  )
}

/** Arrived within the last day. */
const isFresh = (at: string) => Date.now() - Date.parse(at) < 24 * 60 * 60 * 1000

/** An episode as the history names it, without the show's title: "S02E03 - Who Is Alive?". */
const episodeName = (s: FrontStory, title: string) =>
  title.startsWith(s.title + ' ') ? title.slice(s.title.length + 1) : title

/** What arrived and when: "S02E03 - Who Is Alive? · arrived today at 21:14". */
function kicker(s: FrontStory): string {
  const what =
    s.kind === 'movie'
      ? s.year > 0
        ? `Film, ${s.year}`
        : 'Film'
      : s.arrived.length === 1
        ? episodeName(s, s.arrived[0])
        : `${s.arrived.length} new episodes`
  return `${what} · arrived ${when(s.at)}`
}

function when(at: string): string {
  const d = new Date(at)
  const time = d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })
  const days = Math.round((startOfDay(new Date()) - startOfDay(d)) / 86400000)
  if (days === 0) return `today at ${time}`
  if (days === 1) return `yesterday at ${time}`
  if (days < 7) return `on ${d.toLocaleDateString('en-GB', { weekday: 'long' })}`
  return `on ${d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short' })}`
}

const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()

/** The strip of running downloads at the top of the library. */
function Tonight({ titles }: { titles: Map<number, string> }) {
  const status = useStatus()
  const action = useAction()
  if (!status) return null
  const active = status.status.active
  const search = status.status.search
  const shown = active.slice(0, search.current ? 2 : 3)
  const waiting = search.queued > 0 ? `${search.queued} more waiting` : ''
  const queued = status.status.queued.length

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
          {queued > 0 && `, ${queued} queued`}
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
          <Busy on={action.busy} label="Starting…">
            Search for wanted now
          </Busy>
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

  // A cinema ticket: the poster on a stub, coloured by state, torn off along
  // the dashed line; punched once the movie is in the library.
  return (
    <article className={`movie-ticket ticket-${state}`}>
      <a className="ticket-poster" href={href(`/media/${media.id}`)} tabIndex={-1} aria-hidden="true">
        <Poster src={media.poster} title={media.title} size="sm" />
      </a>
      {media.arrivedAt && (
        <span className="stamp stamp-new" title={`Arrived ${new Date(media.arrivedAt).toLocaleString('en-GB')}`}>
          New
        </span>
      )}
      <div className="movie-body">
        <span className="admit" aria-hidden="true">
          Admit one · No. {String(media.id).padStart(3, '0')}
        </span>
        <a className="movie-title" href={href(`/media/${media.id}`)}>
          {media.title} {media.year > 0 && <span className="muted">({media.year})</span>}
        </a>
        <StateBadge state={state} label={label} />
        {action.error && <span className="error-text">{action.error}</span>}
      </div>
      {state === 'imported' && <span className="punch" aria-hidden="true" />}
      {state === 'downloading' && p?.hasInfo && (
        <span className="ticket-progress" aria-hidden="true">
          <span style={{ width: `${p.percent}%` }} />
        </span>
      )}
      {state === 'ignored' && (
        <button className="button small" disabled={action.busy} onClick={() => want(true)}>
          <Busy on={action.busy}>Download</Busy>
        </button>
      )}
      {state === 'wanted' && (
        <button className="button small" disabled={action.busy} onClick={() => want(false)}>
          <Busy on={action.busy}>Stop wanting</Busy>
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
