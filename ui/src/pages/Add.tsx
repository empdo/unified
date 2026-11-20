import { useState } from 'react'
import { api, type FindResult, type Kind } from '../api'
import { Notice, Poster, Spinner } from '../components'
import { useAction, useData } from '../hooks'
import { href, navigate } from '../router'

export default function Add({ kind, query }: { kind: Kind; query: string }) {
  const results = useData(() => (query ? api.find(kind, query) : Promise.resolve([])), [kind, query])

  return (
    <>
      <div className="page-head">
        <h1>
          {kind === 'movie' ? 'Movies' : 'Shows'} matching “{query}”
        </h1>
      </div>
      {results.error && (
        <Notice kind="error">
          {results.error}
          {/tmdb/i.test(results.error) && (
            <>
              {' '}
              Check the TMDB key in <a href={href('/settings')}>Settings</a>.
            </>
          )}
        </Notice>
      )}
      {results.loading && <Spinner label="Searching TMDB…" />}
      {results.data?.length === 0 && <p className="muted">No matches on TMDB.</p>}
      <div className="results">
        {results.data?.map((r) => (
          <Result key={r.tmdbId} kind={kind} result={r} />
        ))}
      </div>
    </>
  )
}

export function Result({ kind, result }: { kind: Kind; result: FindResult }) {
  const action = useAction()
  const [wantAll, setWantAll] = useState(true)
  const add = async () => {
    let id = 0
    const ok = await action.run(async () => {
      id = (await api.addMedia(kind, result.tmdbId, kind === 'movie' || wantAll)).id
    })
    if (ok) navigate(`/media/${id}`)
  }
  return (
    <article className="result">
      <Poster src={result.poster} title={result.title} />
      <div className="result-body">
        <h2>
          {result.title} {result.year > 0 && <span className="muted">{result.year}</span>}
        </h2>
        {result.overview && <p className="overview clamp">{result.overview}</p>}
        <div className="actions">
          {result.libraryId ? (
            <a className="button" href={href(`/media/${result.libraryId}`)}>
              In library →
            </a>
          ) : (
            <>
              {kind === 'show' && (
                <select value={wantAll ? 'all' : 'none'} onChange={(e) => setWantAll(e.target.value === 'all')}>
                  <option value="all">Download every episode</option>
                  <option value="none">Choose seasons after adding</option>
                </select>
              )}
              <button className="button primary" onClick={add} disabled={action.busy}>
                {action.busy ? 'Adding…' : 'Add'}
              </button>
            </>
          )}
        </div>
        {action.error && <Notice kind="error">{action.error}</Notice>}
      </div>
    </article>
  )
}
