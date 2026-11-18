import { useState } from 'react'
import { api, type Item, type MediaDetail } from './api'
import { Notice, StateBadge } from './components'
import { airDate, episodeCode, plural } from './format'
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
export function ShowSeasons(props: { media: MediaDetail; reload: () => void }) {
  const { media, reload } = props
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
      <SeasonPanel key={current.number} media={media} season={current} reload={reload} />
    </>
  )
}

function SeasonPanel(props: { media: MediaDetail; season: Season; reload: () => void }) {
  const { media, season, reload } = props
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

  const setSeason = (want: boolean) => action.run(() => api.wantSeason(media.id, season.number, want)).then(reload)
  const setEpisode = (e: Item, want: boolean) => action.run(() => api.wantItem(e.id, want)).then(reload)

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
          {wanted > 0 && (
            <button className="button" disabled={action.busy} onClick={() => setSeason(false)}>
              Stop wanting season
            </button>
          )}
          {ignored > 0 && (
            <button className="button ink" disabled={action.busy} onClick={() => setSeason(true)}>
              Download whole season
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
              <StateBadge state={e.state} />
            </span>
            <span className="ep-actions">
              {e.state === 'ignored' && (
                <button className="button small" disabled={action.busy} onClick={() => setEpisode(e, true)}>
                  Download
                </button>
              )}
              {e.state === 'wanted' && (
                <button className="button small" disabled={action.busy} onClick={() => setEpisode(e, false)}>
                  Stop wanting
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
