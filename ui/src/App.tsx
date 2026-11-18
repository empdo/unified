import { useState, type FormEvent } from 'react'
import { api, type Kind } from './api'
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
      page = <ReleasesPage params={route.params} />
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
          <SearchBox
            key={route.params.get('q') ?? ''}
            kind={section === 'add' && route.params.get('kind') === 'show' ? 'show' : 'movie'}
            query={section === 'add' ? (route.params.get('q') ?? '') : ''}
          />
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

function SearchBox(props: { kind: Kind; query: string }) {
  const [kind, setKind] = useState<Kind>(props.kind)
  const [query, setQuery] = useState(props.query)
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (query.trim()) navigate(`/add?kind=${kind}&q=${encodeURIComponent(query.trim())}`)
  }
  return (
    <form className="search" onSubmit={submit} role="search">
      <div className="segmented" role="radiogroup" aria-label="Type">
        {(['movie', 'show'] as const).map((k) => (
          <button key={k} type="button" role="radio" aria-checked={kind === k} onClick={() => setKind(k)}>
            {k === 'movie' ? 'Movie' : 'Show'}
          </button>
        ))}
      </div>
      <input
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        placeholder={kind === 'movie' ? 'Find a movie to add…' : 'Find a show to add…'}
        aria-label="Title to add"
      />
      <button className="button primary" type="submit">
        Search
      </button>
    </form>
  )
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
