import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent, type ReactNode } from 'react'
import { api } from './api'
import { Poster } from './components'
import { useData } from './hooks'
import { href, navigate, useRoute } from './router'
import { StatusContext, useStatus } from './status'
import Library from './pages/Library'
import MediaPage from './pages/Media'
import Add from './pages/Add'
import ReleasesPage from './pages/Releases'
import Downloads from './pages/Downloads'
import SettingsPage from './pages/Settings'

export default function App() {
  const route = useRoute()
  const status = useData(api.status, [], 3000)
  const [section] = route.parts

  let page
  switch (section) {
    case undefined:
      page = <Library />
      break
    case 'media':
      page = <MediaPage id={Number(route.parts[1])} />
      break
    case 'add':
      page = <Add kind={route.params.get('kind') === 'show' ? 'show' : 'movie'} query={route.params.get('q') ?? ''} />
      break
    case 'releases':
      page = <ReleasesPage key={route.params.toString()} params={route.params} />
      break
    case 'downloads':
      page = <Downloads />
      break
    case 'settings':
      page = <SettingsPage />
      break
    default:
      page = <p className="muted">Page not found.</p>
  }

  const active = status.data?.status.active.length ?? 0
  return (
    <StatusContext.Provider value={status.data}>
      <header className="masthead">
        <div className="masthead-top">
          <a className="brand" href={href('/')}>
            unified
          </a>
          <span className="dateline">{today()} · TV &amp; film</span>
          <SearchBox key={route.params.get('q') ?? ''} query={section === 'add' ? (route.params.get('q') ?? '') : ''} />
        </div>
        <nav className="tabs">
          <a className={section === undefined || section === 'media' ? 'current' : ''} href={href('/')}>
            Library
          </a>
          <a className={section === 'downloads' ? 'current' : ''} href={href('/downloads')}>
            Downloads{active > 0 && ` (${active})`}
          </a>
          <a className={section === 'settings' ? 'current' : ''} href={href('/settings')}>
            Settings
          </a>
          <VpnStatus />
        </nav>
      </header>
      {status.error && <div className="offline">Can't reach unified: {status.error}</div>}
      <main>{page}</main>
    </StatusContext.Provider>
  )
}

/**
 * Searches the library as you type; the dropdown links to matching movies and
 * shows, and offers to look up the title on TMDB to add something new.
 */
function SearchBox(props: { query: string }) {
  const [query, setQuery] = useState(props.query)
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(0)
  const library = useData(api.library, [])
  const box = useRef<HTMLFormElement>(null)

  useEffect(() => {
    if (!open) return
    const close = (e: MouseEvent) => {
      if (!box.current?.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', close)
    return () => document.removeEventListener('mousedown', close)
  }, [open])

  const q = query.trim()
  const needle = q.toLowerCase()
  const matches = (library.data ?? [])
    .filter((m) => !needle || m.title.toLowerCase().includes(needle))
    .sort((a, b) => rank(a.title, needle) - rank(b.title, needle) || a.title.localeCompare(b.title))
    .slice(0, 12)

  type Option = { key: string; go: () => void }
  const options: Option[] = [
    ...matches.map((m) => ({ key: `m${m.id}`, go: () => navigate(`/media/${m.id}`) })),
    ...(q
      ? (['movie', 'show'] as const).map((k) => ({
          key: k,
          go: () => navigate(`/add?kind=${k}&q=${encodeURIComponent(q)}`),
        }))
      : []),
  ]
  const choose = (o: Option | undefined) => {
    if (!o) return
    setOpen(false)
    o.go()
  }

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (open || q) choose(open ? options[active] : options[0])
  }
  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      setOpen(true)
      if (options.length) setActive((a) => (a + (e.key === 'ArrowDown' ? 1 : options.length - 1)) % options.length)
    } else if (e.key === 'Escape') {
      setOpen(false)
    }
  }

  const option = (i: number, children: ReactNode, className?: string) => (
    <li
      key={options[i].key}
      id={`search-${options[i].key}`}
      role="option"
      aria-selected={i === active}
      className={className}
      onMouseEnter={() => setActive(i)}
      onMouseDown={(e) => e.preventDefault()}
      onClick={() => choose(options[i])}
    >
      {children}
    </li>
  )

  return (
    <form className="search" onSubmit={submit} role="search" ref={box}>
      <input
        value={query}
        onChange={(e) => {
          setQuery(e.target.value)
          setActive(0)
          setOpen(true)
        }}
        onFocus={() => {
          library.reload()
          setOpen(true)
        }}
        onKeyDown={onKey}
        placeholder="Search your library…"
        aria-label="Search your library"
        role="combobox"
        aria-expanded={open}
        aria-controls="search-menu"
        aria-activedescendant={open && options[active] ? `search-${options[active].key}` : undefined}
        autoComplete="off"
      />
      <button className="button primary" type="submit">
        Search
      </button>
      {open && (library.data || q) && (
        <ul className="search-menu" id="search-menu" role="listbox">
          {matches.map((m, i) =>
            option(
              i,
              <>
                <Poster src={m.poster} title={m.title} size="xs" />
                <span className="search-title">
                  {m.title} {m.year > 0 && <span className="muted">({m.year})</span>}
                </span>
                <span className="search-kind">{m.kind === 'movie' ? 'Movie' : 'Show'}</span>
              </>,
            ),
          )}
          {matches.length === 0 && (
            <li className="search-empty">
              {library.data?.length === 0 ? 'Your library is empty.' : 'Nothing in your library matches.'}
            </li>
          )}
          {q && (
            <>
              <li className="search-sep" aria-hidden="true" />
              {option(matches.length, <>Find movies matching “{q}” to add</>, 'search-add')}
              {option(matches.length + 1, <>Find shows matching “{q}” to add</>, 'search-add')}
            </>
          )}
        </ul>
      )}
    </form>
  )
}

/** Orders matches: titles starting with the query first, then word starts, then the rest. */
function rank(title: string, needle: string) {
  const t = title.toLowerCase()
  if (!needle || t.startsWith(needle)) return 0
  if (t.includes(' ' + needle)) return 1
  return 2
}

const today = () => new Date().toLocaleDateString('en-GB', { weekday: 'long', day: 'numeric', month: 'long' })

function VpnStatus() {
  const s = useStatus()?.status
  if (!s) return null
  const [state, label, text] = s.vpnError
    ? ['down', 'VPN down', `VPN down: ${s.vpnError}`]
    : s.vpn
      ? ['up', 'VPN connected', `Torrent traffic uses ${s.vpn}`]
      : ['off', 'No VPN', 'Torrent traffic is not bound to a VPN']
  return (
    <a className={`vpn vpn-${state}`} href={href('/downloads')} title={text}>
      <span className="dot" />
      {label}
    </a>
  )
}
