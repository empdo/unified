import { useState, type FormEvent } from 'react'
import { api, type BrowseList, type Kind, type ListKind, type ListWant, type WatchList } from '../api'
import { Busy, Notice, Spinner } from '../components'
import { plural } from '../format'
import { useAction, useData } from '../hooks'
import { href } from '../router'
import { Result } from './Add'

const BROWSE: { key: BrowseList; label: string; show?: string }[] = [
  { key: 'trending', label: 'Trending this week' },
  { key: 'popular', label: 'Popular' },
  { key: 'top_rated', label: 'Top rated' },
  { key: 'upcoming', label: 'Coming soon', show: 'On the air' },
]

export default function DiscoverPage({ view, params }: { view: 'browse' | 'lists'; params: URLSearchParams }) {
  return (
    <>
      <div className="page-head">
        <h1>Discover</h1>
        <div className="segmented" role="radiogroup" aria-label="View">
          <a role="radio" className="segmented-link" aria-checked={view === 'browse'} href={href('/discover')}>
            Browse
          </a>
          <a role="radio" className="segmented-link" aria-checked={view === 'lists'} href={href('/discover/lists')}>
            Lists
          </a>
        </div>
      </div>
      {view === 'browse' ? <Browse params={params} /> : <Lists />}
    </>
  )
}

function Browse({ params }: { params: URLSearchParams }) {
  const kind: Kind = params.get('kind') === 'show' ? 'show' : 'movie'
  const list = (BROWSE.find((b) => b.key === params.get('list'))?.key ?? 'trending') as BrowseList
  const page = Math.max(1, Number(params.get('page')) || 1)
  const results = useData(() => api.discover(kind, list, page), [kind, list, page])
  const to = (change: Record<string, string | number>) => {
    const next = new URLSearchParams({ kind, list, page: '1' })
    for (const [k, v] of Object.entries(change)) next.set(k, String(v))
    return href(`/discover?${next}`)
  }
  const pages = Math.min(results.data?.pages ?? 1, 500) // TMDB serves at most 500 pages

  return (
    <>
      <div className="discover-controls">
        <div className="segmented" role="radiogroup" aria-label="Kind">
          {(['movie', 'show'] as const).map((k) => (
            <a key={k} role="radio" className="segmented-link" aria-checked={k === kind} href={to({ kind: k })}>
              {k === 'movie' ? 'Movies' : 'Shows'}
            </a>
          ))}
        </div>
        <div className="pills" role="tablist" aria-label="List">
          {BROWSE.map((b) => (
            <a key={b.key} role="tab" className="pill" aria-selected={b.key === list} href={to({ list: b.key })}>
              {(kind === 'show' && b.show) || b.label}
            </a>
          ))}
        </div>
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
      {results.loading && <Spinner label="Asking TMDB…" />}
      <div className="results">
        {results.data?.results.map((r) => (
          <Result key={r.tmdbId} kind={kind} result={r} />
        ))}
      </div>
      {results.data && pages > 1 && (
        <nav className="pager" aria-label="Pages">
          {page > 1 && (
            <a className="button" href={to({ page: page - 1 })}>
              ← Previous
            </a>
          )}
          <span className="muted small">
            Page {page} of {pages}
          </span>
          {page < pages && (
            <a className="button" href={to({ page: page + 1 })}>
              Next →
            </a>
          )}
        </nav>
      )}
    </>
  )
}

const KINDS: { key: ListKind; label: string; placeholder: string; help: string }[] = [
  {
    key: 'trakt-watchlist',
    label: 'Trakt watchlist',
    placeholder: 'Trakt user name',
    help: 'The user name, or the address of the watchlist. The profile must be public.',
  },
  {
    key: 'trakt-list',
    label: 'Trakt list',
    placeholder: 'https://trakt.tv/users/someone/lists/favourites',
    help: 'The address of the list, or user/list-name. The list must be public.',
  },
  {
    key: 'tmdb-list',
    label: 'TMDB list',
    placeholder: 'https://www.themoviedb.org/list/8123',
    help: 'The address of a public list on themoviedb.org, or its number.',
  },
]

const WANTS: { key: ListWant; label: string }[] = [
  { key: 'all', label: 'Download movies and every episode' },
  { key: 'first', label: 'Download movies and the first season of shows' },
  { key: 'none', label: "Add to the library, but don't download" },
]

function Lists() {
  const lists = useData(api.lists, [])
  const data = lists.data
  return (
    <>
      <p className="muted lists-intro">
        Movies and shows on these lists are added to the library as they appear on them: now, and then every six
        hours. Something you remove from the library is not added again.
      </p>
      {lists.error && <Notice kind="error">{lists.error}</Notice>}
      {!data && !lists.error && <Spinner label="Loading lists…" />}
      {data && (
        <>
          {data.lists.length > 0 && (
            <ul className="list">
              {data.lists.map((l) => (
                <ListRow key={l.id} list={l} reload={lists.reload} />
              ))}
            </ul>
          )}
          <AddList trakt={data.trakt} reload={lists.reload} />
        </>
      )}
    </>
  )
}

function ListRow({ list: l, reload }: { list: WatchList; reload: () => void }) {
  const action = useAction()
  const [added, setAdded] = useState<string[]>()
  const sync = () =>
    action.run(async () => {
      const r = await api.syncList(l.id)
      setAdded(r.added)
      if (r.error) throw new Error(r.error)
    }, 'sync').then(reload)
  const remove = () => {
    if (confirm(`Stop following ${l.name}? What it added stays in the library.`))
      action.run(() => api.removeList(l.id), 'remove').then(reload)
  }
  const kind = KINDS.find((k) => k.key === l.kind)?.label ?? l.kind
  const want = WANTS.find((w) => w.key === l.want)?.label ?? l.want
  const synced = l.syncedAt
    ? `synced ${new Date(l.syncedAt).toLocaleString('en-GB', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' })}`
    : 'not synced yet'
  return (
    <li className="watchlist">
      <div className="watchlist-body">
        <span className="watchlist-name">{l.name || l.source}</span>
        <span className="small muted">
          {kind} · {want} · {synced}
        </span>
        {l.lastError && <span className="error-text">{l.lastError}</span>}
        {added && (
          <span className="small ok">
            {added.length === 0 ? 'Nothing new.' : `Added ${added.join(', ')}.`}
          </span>
        )}
        {action.error && <span className="error-text">{action.error}</span>}
      </div>
      <div className="actions">
        <button className="button small" disabled={action.busy} onClick={sync}>
          <Busy on={action.pending === 'sync'} label="Syncing…">
            Sync now
          </Busy>
        </button>
        <button className="button small danger" disabled={action.busy} onClick={remove}>
          <Busy on={action.pending === 'remove'}>Remove</Busy>
        </button>
      </div>
    </li>
  )
}

function AddList({ trakt, reload }: { trakt: boolean; reload: () => void }) {
  const [kind, setKind] = useState<ListKind>('trakt-watchlist')
  const [source, setSource] = useState('')
  const [want, setWant] = useState<ListWant>('all')
  const [added, setAdded] = useState<string[]>()
  const action = useAction()
  const k = KINDS.find((x) => x.key === kind)!
  const needsTrakt = kind.startsWith('trakt') && !trakt

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const ok = await action.run(async () => {
      const r = await api.addList(kind, source, want)
      setAdded(r.added)
      if (r.error) throw new Error(r.error)
    })
    if (ok) setSource('')
    reload()
  }

  return (
    <form className="panel add-list" onSubmit={submit}>
      <h2>Follow a list</h2>
      <div className="field-row">
        <label className="field">
          <span className="field-label">Kind</span>
          <select value={kind} onChange={(e) => setKind(e.target.value as ListKind)}>
            {KINDS.map((x) => (
              <option key={x.key} value={x.key}>
                {x.label}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          <span className="field-label">What to do with new entries</span>
          <select value={want} onChange={(e) => setWant(e.target.value as ListWant)}>
            {WANTS.map((w) => (
              <option key={w.key} value={w.key}>
                {w.label}
              </option>
            ))}
          </select>
        </label>
      </div>
      <label className="field">
        <span className="field-label">List</span>
        <input value={source} onChange={(e) => setSource(e.target.value)} placeholder={k.placeholder} required />
        <span className="muted small">{k.help}</span>
      </label>
      {needsTrakt && (
        <Notice kind="warn">
          Trakt lists need a Trakt client ID. Add one in <a href={href('/settings')}>Settings</a>.
        </Notice>
      )}
      {action.error && <Notice kind="error">{action.error}</Notice>}
      {added && !action.error && (
        <Notice>
          {added.length === 0
            ? 'Following it. Everything on it is in the library already.'
            : `Following it. Added ${plural(added.length, 'movie or show', 'movies and shows')}: ${added.join(', ')}.`}
        </Notice>
      )}
      <button className="button primary" type="submit" disabled={action.busy || needsTrakt || !source.trim()}>
        <Busy on={action.busy} label="Adding…">
          Follow
        </Busy>
      </button>
    </form>
  )
}
