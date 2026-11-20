import { useState } from 'react'
import { api, type ScanEntry, type ScanResult } from '../api'
import { Busy, Notice, Poster } from '../components'
import { bytes, plural } from '../format'
import { useAction } from '../hooks'
import { href } from '../router'

/**
 * Finds movies and shows already in the library folders, so they count as
 * in the library and are not downloaded again. Nothing is added until the
 * matches are confirmed.
 */
export default function ScanPage() {
  const scan = useAction()
  const save = useAction()
  const [found, setFound] = useState<{ entries: ScanEntry[]; moviesDir: string; tvDir: string }>()
  // The TMDB id picked for each entry, by path; 0 leaves it out.
  const [picks, setPicks] = useState<Record<string, number>>({})
  const [wantMissing, setWantMissing] = useState(false)
  const [results, setResults] = useState<ScanResult[]>()

  const run = () =>
    scan.run(async () => {
      const r = await api.scan()
      setFound(r)
      setResults(undefined)
      setPicks(Object.fromEntries(r.entries.map((e) => [e.path, e.matches[0]?.tmdbId ?? 0])))
    })

  const entries = found?.entries.filter((e) => e.known < e.files) ?? []
  const complete = (found?.entries.length ?? 0) - entries.length
  const chosen = entries.filter((e) => picks[e.path])

  const add = () =>
    save.run(async () => {
      const res = await api.scanImport(
        chosen.map((e) => ({ kind: e.kind, path: e.path, tmdbId: picks[e.path] })),
        wantMissing,
      )
      setResults(res)
      setFound(undefined)
    })

  return (
    <>
      <p>
        <a className="back" href={href('/')}>
          ← Library
        </a>
      </p>
      <div className="page-head">
        <h1>Find what you have</h1>
        <button className="button primary" disabled={scan.busy || save.busy} onClick={run}>
          <Busy on={scan.busy} label="Scanning…">
            {found ? 'Scan again' : 'Scan folders'}
          </Busy>
        </button>
      </div>
      <p className="muted scan-intro">
        Looks through the movie and TV folders for what is already there and matches it on TMDB, so it shows as in
        the library and isn't downloaded again. Each folder in the movie folder is a movie, and each folder in the TV
        folder a show, best named <code>Title (Year)</code>. Files stay where they are.
      </p>
      {scan.busy && <p className="muted small">Looking up every folder on TMDB; a big library takes a minute.</p>}
      {scan.error && <Notice kind="error">{scan.error}</Notice>}

      {results && <Results results={results} />}

      {found && (
        <>
          <p className="muted small">
            In <code>{found.moviesDir}</code> and <code>{found.tvDir}</code>:{' '}
            {entries.length === 0
              ? 'nothing the library lacks.'
              : `${plural(entries.length, 'movie or show', 'movies and shows')} the library lacks files of.`}
            {complete > 0 && ` ${complete} more are in the library already.`}
          </p>
          {entries.length > 0 && (
            <>
              <ul className="list scan-list">
                {entries.map((e) => (
                  <Entry
                    key={e.path}
                    entry={e}
                    pick={picks[e.path] ?? 0}
                    setPick={(id) => setPicks({ ...picks, [e.path]: id })}
                  />
                ))}
              </ul>
              <div className="savebar scan-bar">
                <label className="check">
                  <input type="checkbox" checked={wantMissing} onChange={(e) => setWantMissing(e.target.checked)} />
                  Also download the episodes and movies that have no file
                </label>
                <button className="button primary" disabled={save.busy || chosen.length === 0} onClick={add}>
                  <Busy on={save.busy} label="Adding…">
                    Add {chosen.length} to the library
                  </Busy>
                </button>
              </div>
              {save.error && <Notice kind="error">{save.error}</Notice>}
            </>
          )}
        </>
      )}
    </>
  )
}

function Entry({ entry: e, pick, setPick }: { entry: ScanEntry; pick: number; setPick: (id: number) => void }) {
  const match = e.matches.find((m) => m.tmdbId === pick)
  const what =
    e.kind === 'movie'
      ? `${plural(e.files, 'file')}, ${bytes(e.size)}`
      : [
          plural(e.episodes, 'episode'),
          bytes(e.size),
          e.unknown.length > 0 && `${plural(e.unknown.length, 'file')} not recognized`,
        ]
          .filter(Boolean)
          .join(', ')
  return (
    <li className="scan-entry">
      <Poster src={match?.poster ?? ''} title={match?.title ?? e.title} size="xs" />
      <div className="scan-found">
        <span className="scan-name" title={e.path}>
          {e.name}
        </span>
        <span className="small muted">
          {e.kind === 'movie' ? 'Movie' : 'Show'} · {what}
          {e.known > 0 && ` · ${e.known} in the library already`}
        </span>
        {e.unknown.length > 0 && (
          <details className="small muted">
            <summary>Files not recognized</summary>
            <ul>
              {e.unknown.slice(0, 20).map((u) => (
                <li key={u}>{u}</li>
              ))}
            </ul>
            Name them like <code>Show - S01E02.mkv</code> to have them found.
          </details>
        )}
      </div>
      <div className="scan-match">
        {e.error ? (
          <span className="error-text">{e.error}</span>
        ) : e.matches.length === 0 ? (
          <span className="muted small">No match on TMDB; rename the folder to Title (Year)</span>
        ) : (
          <select
            value={pick}
            onChange={(ev) => setPick(Number(ev.target.value))}
            aria-label={`TMDB match for ${e.name}`}
          >
            {e.matches.map((m) => (
              <option key={m.tmdbId} value={m.tmdbId}>
                {m.title}
                {m.year > 0 && ` (${m.year})`}
                {m.libraryId ? ' · in library' : ''}
              </option>
            ))}
            <option value={0}>Leave out</option>
          </select>
        )}
      </div>
    </li>
  )
}

function Results({ results }: { results: ScanResult[] }) {
  const ok = results.filter((r) => !r.error)
  const files = ok.reduce((n, r) => n + r.imported, 0)
  return (
    <section>
      <Notice kind={ok.length === results.length ? 'info' : 'warn'}>
        Added {plural(ok.length, 'movie or show', 'movies and shows')} with {plural(files, 'file')}.
        {ok.length < results.length && ` ${results.length - ok.length} could not be added.`}
      </Notice>
      <ul className="list">
        {results.map((r) => (
          <li key={r.path}>
            {r.mediaId ? (
              <a href={href(`/media/${r.mediaId}`)} className="release-name">
                {r.title}
              </a>
            ) : (
              <span className="release-name">{r.path}</span>
            )}
            <span className={r.error ? 'error-text' : 'small muted'}>{r.error ?? plural(r.imported, 'file')}</span>
          </li>
        ))}
      </ul>
    </section>
  )
}
