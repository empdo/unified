import { useState } from 'react'
import { api, type Candidate } from '../api'
import { Notice, Spinner } from '../components'
import { bytes } from '../format'
import { useAction, useData } from '../hooks'
import { href } from '../router'

export default function ReleasesPage({ params }: { params: URLSearchParams }) {
  const media = Number(params.get('media'))
  const season = params.has('season') ? Number(params.get('season')) : undefined
  const episode = params.has('episode') ? Number(params.get('episode')) : undefined
  const releases = useData(() => api.releases(media, season, episode), [media, season, episode])
  const r = releases.data
  const accepted = r?.candidates.filter((c) => !c.reject).length ?? 0
  const grab = useAction()
  const [grabbed, setGrabbed] = useState<string>()

  const pick = async (c: Candidate) => {
    if (c.reject && !confirm(`This release was rejected: ${c.reject}. Grab it anyway?`)) return
    setGrabbed(undefined)
    if (await grab.run(() => api.grab(media, season ?? 0, episode ?? 0, c))) setGrabbed(c.title)
  }

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
      {grab.error && <Notice kind="error">Could not grab it: {grab.error}</Notice>}
      {grabbed && (
        <Notice>
          Grabbed {grabbed}. Follow it on the <a href={href('/downloads')}>Downloads</a> page.
        </Notice>
      )}
      {r && (
        <>
          <p className="muted small">
            {r.candidates.length} found, {accepted} acceptable. When something is wanted, unified grabs the top
            acceptable release by itself; Grab picks one yourself, even a rejected one.
          </p>
          <div className="table-wrap">
            <table className="releases">
              <thead>
                <tr>
                  <th>Release</th>
                  <th className="right">Size</th>
                  <th className="right">Seeds</th>
                  <th>Verdict</th>
                  <th />
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
                    <td>
                      <button className="button small" disabled={grab.busy} onClick={() => pick(c)}>
                        Grab
                      </button>
                    </td>
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
