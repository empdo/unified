import { useState } from 'react'
import { api, type Item, type MediaDetail } from '../api'
import { Notice, Poster, ProgressBar, Spinner, StateBadge, progressText } from '../components'
import { episodeCode, plural } from '../format'
import { useAction, useData } from '../hooks'
import { href, navigate } from '../router'
import { useActiveByMedia } from '../status'

export default function MediaPage({ id }: { id: number }) {
  const media = useData(() => api.media(id), [id], 5000)
  const action = useAction()
  const downloads = useActiveByMedia().get(id) ?? []

  if (media.error && !media.data) return <Notice kind="error">{media.error}</Notice>
  if (!media.data) return <Spinner label="Loading…" />
  const m = media.data

  const remove = async () => {
    if (!confirm(`Remove ${m.title} from the library? Downloaded files are kept.`)) return
    if (await action.run(() => api.removeMedia(m.id))) navigate('/')
  }

  return (
    <>
      <p>
        <a className="back" href={href('/')}>
          ← Library
        </a>
      </p>
      <section className="hero">
        <Poster src={m.poster} title={m.title} size="lg" />
        <div className="hero-body">
          <div className="eyebrow">{m.kind === 'movie' ? 'Movie' : 'TV show'}</div>
          <h1>
            {m.title} {m.year > 0 && <span className="muted">{m.year}</span>}
          </h1>
          {m.overview && <p className="overview">{m.overview}</p>}
          {m.movie && <MovieStatus item={m.movie} />}
          {downloads.map((p) => (
            <div className="download-inline" key={p.id}>
              <div className="small muted">{p.name}</div>
              <ProgressBar percent={p.percent} />
              <div className="small muted">{progressText(p)}</div>
            </div>
          ))}
          <div className="actions">
            {m.movie && <MovieWantButton media={media} item={m.movie} />}
            {m.movie && (
              <a className="button" href={href(`/releases?media=${m.id}`)}>
                Browse releases
              </a>
            )}
            <button className="button danger" onClick={remove} disabled={action.busy}>
              Remove
            </button>
          </div>
          {action.error && <Notice kind="error">{action.error}</Notice>}
        </div>
      </section>
      {m.seasons.map((s) => (
        <Season key={s.number} media={m} number={s.number} episodes={s.episodes} reload={media.reload} />
      ))}
    </>
  )
}

function MovieStatus({ item }: { item: Item }) {
  return (
    <div className="movie-status">
      <StateBadge state={item.state} />
      {item.path && <code className="path">{item.path}</code>}
    </div>
  )
}

function MovieWantButton({ media, item }: { media: { reload: () => void }; item: Item }) {
  const action = useAction()
  if (item.state !== 'ignored' && item.state !== 'wanted') return null
  const want = item.state === 'ignored'
  return (
    <button
      className={want ? 'button primary' : 'button'}
      disabled={action.busy}
      onClick={() => action.run(() => api.wantItem(item.id, want)).then(media.reload)}
    >
      {want ? 'Download' : 'Stop wanting'}
    </button>
  )
}

function Season(props: { media: MediaDetail; number: number; episodes: Item[]; reload: () => void }) {
  const { media, number, episodes, reload } = props
  const action = useAction()
  const [open, setOpen] = useState(() => episodes.some((e) => e.state !== 'ignored'))
  const count = (state: string) => episodes.filter((e) => e.state === state).length
  const imported = count('imported')
  const wanted = count('wanted')
  const ignored = count('ignored')

  const setSeason = (want: boolean) => action.run(() => api.wantSeason(media.id, number, want)).then(reload)
  const setEpisode = (e: Item, want: boolean) => action.run(() => api.wantItem(e.id, want)).then(reload)

  return (
    <section className="season">
      <div className="season-head">
        <button className="season-toggle" onClick={() => setOpen(!open)} aria-expanded={open}>
          <span className={`chevron ${open ? 'open' : ''}`} aria-hidden="true">
            ›
          </span>
          <h2>Season {number}</h2>
          <span className="muted small">
            {imported} of {plural(episodes.length, 'episode')} in library
            {wanted > 0 && ` · ${wanted} wanted`}
          </span>
        </button>
        <div className="actions">
          <a className="button small" href={href(`/releases?media=${media.id}&season=${number}`)}>
            Season releases
          </a>
          {wanted > 0 && (
            <button className="button small" disabled={action.busy} onClick={() => setSeason(false)}>
              Stop wanting
            </button>
          )}
          {ignored > 0 && (
            <button className="button small primary" disabled={action.busy} onClick={() => setSeason(true)}>
              Download season
            </button>
          )}
        </div>
      </div>
      {action.error && <Notice kind="error">{action.error}</Notice>}
      {open && (
        <table className="episodes">
          <tbody>
            {episodes.map((e) => (
              <tr key={e.id}>
                <td className="ep">{episodeCode(e.season, e.episode)}</td>
                <td className="ep-title">{e.title || <span className="muted">TBA</span>}</td>
                <td className="muted small nowrap hide-narrow">{e.airDate}</td>
                <td>
                  <StateBadge state={e.state} />
                </td>
                <td className="right nowrap">
                  {(e.state === 'ignored' || e.state === 'wanted') && (
                    <button
                      className="link"
                      disabled={action.busy}
                      onClick={() => setEpisode(e, e.state === 'ignored')}
                    >
                      {e.state === 'ignored' ? 'Want' : 'Unwant'}
                    </button>
                  )}
                  <a
                    className="link"
                    href={href(`/releases?media=${media.id}&season=${e.season}&episode=${e.episode}`)}
                  >
                    Releases
                  </a>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  )
}
