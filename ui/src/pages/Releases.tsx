import { api } from '../api'
import { Notice, Spinner } from '../components'
import { bytes } from '../format'
import { useData } from '../hooks'
import { href } from '../router'

export default function ReleasesPage({ params }: { params: URLSearchParams }) {
  const media = Number(params.get('media'))
  const season = params.has('season') ? Number(params.get('season')) : undefined
  const episode = params.has('episode') ? Number(params.get('episode')) : undefined
  const releases = useData(() => api.releases(media, season, episode), [media, season, episode])
  const r = releases.data
  const accepted = r?.candidates.filter((c) => !c.reject).length ?? 0

  return (
    <>
      <p>
        <a className="back" href={href(`/media/${media}`)}>
          ← Back
        </a>
      </p>
      <div className="page-head">
        <h1>Releases{r && <> for “{r.query}”</>}</h1>
      </div>
      {releases.error && <Notice kind="error">{releases.error}</Notice>}
      {releases.loading && <Spinner label="Searching indexers…" />}
      {r?.errors.map((e) => (
        <Notice kind="warn" key={e}>
          {e}
        </Notice>
      ))}
      {r && (
        <>
          <p className="muted small">
            {r.candidates.length} found, {accepted} acceptable. When something is wanted, unified grabs the top
            acceptable release by itself.
          </p>
          <div className="table-wrap">
            <table className="releases">
              <thead>
                <tr>
                  <th>Release</th>
                  <th className="right">Size</th>
                  <th className="right">Seeds</th>
                  <th>Verdict</th>
                </tr>
              </thead>
              <tbody>
                {r.candidates.map((c) => (
                  <tr key={c.title} className={c.reject ? 'rejected' : ''}>
                    <td>
                      <div className="release-name">{c.title}</div>
                      <div className="chips">
                        {[c.resolution, c.source, c.codec, c.group].filter(Boolean).map((t) => (
                          <span className="chip" key={t}>
                            {t}
                          </span>
                        ))}
                        <span className="muted small">{c.indexer}</span>
                      </div>
                    </td>
                    <td className="right nowrap">{bytes(c.size)}</td>
                    <td className="right">{c.seeders >= 0 ? c.seeders : '?'}</td>
                    <td>{c.reject ? <span className="small">{c.reject}</span> : <span className="ok">Acceptable</span>}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
    </>
  )
}
