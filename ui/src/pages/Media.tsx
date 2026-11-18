import { useState } from 'react'
import { api, type Item, type MediaDetail } from '../api'
import { Busy, Notice, Poster, ProgressBar, Spinner, progressText } from '../components'
import { plural } from '../format'
import { useAction, useData } from '../hooks'
import { href, navigate } from '../router'
import { ItemStatus } from '../itemstate'
import { ShowSeasons } from '../seasons'
import { useActiveByMedia } from '../status'

export default function MediaPage({ id }: { id: number }) {
  const media = useData(() => api.media(id), [id], 5000)
  const [removing, setRemoving] = useState(false)
  const downloads = useActiveByMedia().get(id) ?? []

  if (media.error && !media.data) return <Notice kind="error">{media.error}</Notice>
  if (!media.data) return <Spinner label="Loading…" />
  const m = media.data

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
          {m.movie && downloads.map((p) => (
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
            <button className="button danger" onClick={() => setRemoving(!removing)} aria-expanded={removing}>
              Remove…
            </button>
          </div>
          {removing && <RemovePanel media={m} downloading={downloads.length} close={() => setRemoving(false)} reload={media.reload} />}
        </div>
      </section>
      {m.kind === 'show' && (
        <section className="media-seasons">
          <ShowSeasons media={m} downloads={downloads} reload={media.reload} />
        </section>
      )}
    </>
  )
}

/** The ways to remove a movie or show: its files, it from the library, or both. */
function RemovePanel(props: { media: MediaDetail; downloading: number; close: () => void; reload: () => void }) {
  const { media: m, downloading, close, reload } = props
  const action = useAction()
  const items = m.movie ? [m.movie] : m.seasons.flatMap((s) => s.episodes)
  const files = items.filter((it) => it.state === 'imported').length
  const what = m.kind === 'movie' ? 'the movie' : 'the show'

  const onDisk = [
    files > 0 && (m.movie ? 'its file' : plural(files, 'episode file')),
    downloading > 0 && plural(downloading, 'running download'),
  ]
    .filter(Boolean)
    .join(' and ')

  const deleteFiles = async () => {
    if (await action.run(() => api.deleteFiles(m.id), 'files')) {
      close()
      reload()
    }
  }
  const remove = async (withFiles: boolean) => {
    if (await action.run(() => api.removeMedia(m.id, withFiles), withFiles ? 'all' : 'keep')) navigate('/')
  }

  return (
    <div className="remove-panel" role="region" aria-label={`Remove ${m.title}`}>
      <h3>Remove {m.title}</h3>
      <p className="small muted">
        {onDisk ? `On disk: ${onDisk}.` : 'Nothing of it is on disk yet.'} Deleting files also removes the copies
        in the download folder, so seeding stops.
      </p>
      <ul className="remove-options">
        <li className="remove-option">
          <button className="button danger" disabled={action.busy} onClick={deleteFiles}>
            <Busy on={action.pending === 'files'} label="Deleting…">
              Delete files
            </Busy>
          </button>
          <p>Keep {what} in the library, with nothing wanted, so it isn't downloaded again.</p>
        </li>
        <li className="remove-option">
          <button className="button danger" disabled={action.busy} onClick={() => remove(true)}>
            <Busy on={action.pending === 'all'} label="Deleting…">
              Delete files and remove
            </Busy>
          </button>
          <p>Delete everything on disk and remove {what} from unified.</p>
        </li>
        <li className="remove-option">
          <button className="button" disabled={action.busy} onClick={() => remove(false)}>
            <Busy on={action.pending === 'keep'} label="Removing…">
              Remove, keep files
            </Busy>
          </button>
          <p>Remove {what} from unified and leave its files where they are.</p>
        </li>
      </ul>
      {action.error && <Notice kind="error">{action.error}</Notice>}
      <div>
        <button className="button small" onClick={close} disabled={action.busy}>
          Cancel
        </button>
      </div>
    </div>
  )
}

function MovieStatus({ item }: { item: Item }) {
  return (
    <div className="movie-status">
      <ItemStatus item={item} />
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
      <Busy on={action.busy}>{want ? 'Download' : 'Stop wanting'}</Busy>
    </button>
  )
}
