import { useState } from 'react'
import { api, type Item, type MediaDetail, type Progress } from './api'
import { Busy, Notice } from './components'
import { airDate, episodeCode, plural } from './format'
import { ItemStatus } from './itemstate'
import { useAction } from './hooks'
import { href } from './router'

type Season = MediaDetail['seasons'][number]

const seasonName = (n: number) => (n === 0 ? 'Specials' : `Season ${n}`)

/** The season to open first: the latest one with anything wanted, else the latest regular one. */
function defaultSeason(seasons: Season[]): Season {
  const regular = seasons.filter((s) => s.number > 0)
  const active = regular.filter((s) => s.episodes.some((e) => e.state !== 'ignored'))
  return active.at(-1) ?? regular.at(-1) ?? seasons[0]
}

/** Season tabs plus the chosen season's episodes. */
export function ShowSeasons(props: { media: MediaDetail; downloads: Progress[]; reload: () => void }) {
  const { media, downloads, reload } = props
  const [picked, setPicked] = useState<number>()
  if (media.seasons.length === 0) return <p className="muted">TMDB lists no episodes for this show yet.</p>
  const current = media.seasons.find((s) => s.number === picked) ?? defaultSeason(media.seasons)

  return (
    <>
      {media.seasons.length > 1 && (
        <div className="pills" role="tablist" aria-label="Season">
          {media.seasons.map((s) => (
            <button
              key={s.number}
              type="button"
              role="tab"
              className="pill"
              aria-selected={s.number === current.number}
              onClick={() => setPicked(s.number)}
            >
              {seasonName(s.number)}
            </button>
          ))}
        </div>
      )}
      <SeasonPanel key={current.number} media={media} season={current} downloads={downloads} reload={reload} />
    </>
  )
}

function SeasonPanel(props: { media: MediaDetail; season: Season; downloads: Progress[]; reload: () => void }) {
  const { media, season, downloads, reload } = props
  const action = useAction()
  const count = (state: string) => season.episodes.filter((e) => e.state === state).length
  const total = season.episodes.length
  const imported = count('imported')
  const downloading = count('downloading')
  const wanted = count('wanted')
  const ignored = count('ignored')

  const summary =
    total > 0 && imported === total
      ? 'The whole season is in your library'
      : [
          `${imported} of ${plural(total, 'episode')} in library`,
          downloading > 0 && `${downloading} downloading`,
          wanted > 0 && `${wanted} wanted`,
        ]
          .filter(Boolean)
          .join(' · ')

  const setSeason = (want: boolean) =>
    action.run(() => api.wantSeason(media.id, season.number, want), want ? 'want' : 'stop').then(reload)
  const setEpisode = (e: Item, want: boolean) => action.run(() => api.wantItem(e.id, want), `ep${e.id}`).then(reload)
  const doing = (key: string) => action.pending === key

  const onDisk = imported + downloading
  const deleteSeason = () => {
    const what = [imported > 0 && plural(imported, 'episode file'), downloading > 0 && plural(downloading, 'download')]
      .filter(Boolean)
      .join(' and ')
    if (!confirm(`Delete ${what} of ${seasonName(season.number)}? The episodes won't be downloaded again unless you ask for them.`)) return
    action.run(() => api.deleteSeasonFiles(media.id, season.number), 'delete').then(reload)
  }
  const cancelSeason = () => {
    const rest = wanted > 0 ? ` and stop wanting its other ${plural(wanted, 'missing episode')}` : ''
    if (!confirm(`Cancel the ${plural(downloading, 'downloading episode')} of ${seasonName(season.number)}${rest}? What was downloaded so far is deleted; episodes in your library stay.`)) return
    action.run(() => api.cancelSeason(media.id, season.number), 'cancel').then(reload)
  }
  const deleteEpisode = (e: Item) => {
    const what = e.state === 'downloading' ? 'Stop downloading' : 'Delete the file of'
    if (!confirm(`${what} ${episodeCode(e.season, e.episode)}? It won't be downloaded again unless you ask for it.`)) return
    action.run(() => api.deleteItemFiles(e.id), `ep${e.id}`).then(reload)
  }

  return (
    <div className="season">
      <div className="season-head">
        <div className="season-title">
          <h3>{seasonName(season.number)}</h3>
          <p className="muted small">{summary}</p>
        </div>
        <div className="actions">
          <a className="button" href={href(`/releases?media=${media.id}&season=${season.number}`)}>
            Season releases
          </a>
          {downloading > 0 ? (
            <button className="button" disabled={action.busy} aria-busy={doing('cancel')} onClick={cancelSeason}>
              <Busy on={doing('cancel')} label="Cancelling…">
                Cancel downloads
              </Busy>
            </button>
          ) : (
            wanted > 0 && (
              <button className="button" disabled={action.busy} aria-busy={doing('stop')} onClick={() => setSeason(false)}>
                <Busy on={doing('stop')}>Stop wanting season</Busy>
              </button>
            )
          )}
          {ignored > 0 && (
            <button className="ticket" disabled={action.busy} aria-busy={doing('want')} onClick={() => setSeason(true)}>
              <span className="ticket-stub" aria-hidden="true">
                {season.number === 0 ? 'SP' : `S${String(season.number).padStart(2, '0')}`}
              </span>
              <span className="ticket-main">
                <Busy on={doing('want')} label="Booking…">
                  Download whole season
                </Busy>
              </span>
            </button>
          )}
          {onDisk > 0 && (
            <button className="button danger" disabled={action.busy} aria-busy={doing('delete')} onClick={deleteSeason}>
              <Busy on={doing('delete')} label="Deleting…">
                Delete season files
              </Busy>
            </button>
          )}
        </div>
      </div>
      {action.error && <Notice kind="error">{action.error}</Notice>}
      <ul className="episodes">
        {season.episodes.map((e) => (
          <li key={e.id} className="episode">
            <span className="ep-code">{episodeCode(e.season, e.episode)}</span>
            <span className="ep-title">{e.title || <span className="muted">Title not announced</span>}</span>
            <span className="ep-date">{airDate(e.airDate) || 'Date TBA'}</span>
            <span className="ep-state">
              <ItemStatus item={e} download={downloads.find((p) => p.id === e.downloadId)} />
            </span>
            <span className="ep-actions">
              {e.state === 'ignored' && (
                <button className="button small" disabled={action.busy} onClick={() => setEpisode(e, true)}>
                  <Busy on={doing(`ep${e.id}`)}>Download</Busy>
                </button>
              )}
              {e.state === 'wanted' && (
                <button className="button small" disabled={action.busy} onClick={() => setEpisode(e, false)}>
                  <Busy on={doing(`ep${e.id}`)}>Stop wanting</Busy>
                </button>
              )}
              {(e.state === 'imported' || e.state === 'downloading') && (
                <button className="button small danger" disabled={action.busy} onClick={() => deleteEpisode(e)}>
                  <Busy on={doing(`ep${e.id}`)} label={e.state === 'downloading' ? 'Cancelling…' : 'Deleting…'}>
                    {e.state === 'downloading' ? 'Cancel' : 'Delete'}
                  </Busy>
                </button>
              )}
              <a className="link" href={href(`/releases?media=${media.id}&season=${e.season}&episode=${e.episode}`)}>
                Releases
              </a>
            </span>
          </li>
        ))}
      </ul>
    </div>
  )
}

