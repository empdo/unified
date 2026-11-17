// Types and calls for the unified server's JSON API (api.go).

export type Kind = 'movie' | 'show'
export type ItemState = 'ignored' | 'wanted' | 'downloading' | 'imported'

export interface Progress {
  id: number
  mediaId: number
  name: string
  status: string
  hasInfo: boolean
  percent: number
  done: number
  total: number
  peers: number
  rate: number
}

export interface Status {
  active: Progress[]
  log: string[]
  vpn: string
  vpnError: string
}

export interface StatusResponse {
  status: Status
  recent: { id: number; name: string; state: string }[]
  tmdb: boolean
  indexers: number
}

export interface MediaSummary {
  id: number
  kind: Kind
  tmdbId: number
  title: string
  year: number
  poster: string
  overview?: string
  counts: Partial<Record<ItemState, number>>
}

export interface Item {
  id: number
  season: number
  episode: number
  title: string
  airDate: string
  state: ItemState
  path?: string
}

export interface MediaDetail extends MediaSummary {
  movie?: Item
  seasons: { number: number; episodes: Item[] }[]
}

export interface FindResult {
  tmdbId: number
  title: string
  year: number
  poster: string
  overview: string
  libraryId?: number
}

export interface Candidate {
  title: string
  indexer: string
  size: number
  seeders: number
  resolution: string
  source: string
  codec: string
  group: string
  reject?: string
}

export interface Releases {
  query: string
  errors: string[]
  candidates: Candidate[]
}

export interface Indexer {
  name: string
  url: string
  apiKey: string
}

export interface Quality {
  resolutions: string[]
  preferCodecs: string[]
  maxMovieGB: number
  maxEpisodeGB: number
  minSeeders: number
}

export interface Settings {
  tmdbKey: string
  indexers: Indexer[]
  quality: Quality
  passwordSet: boolean
  passwordFromEnv: boolean
  fixed: {
    dataDir: string
    downloadDir: string
    moviesDir: string
    tvDir: string
    listenPort: number
    vpnInterface: string
    vpnFromEnv: boolean
    configPath: string
  }
}

export interface SettingsUpdate {
  tmdbKey: string
  indexers: Indexer[]
  quality: Quality
  /** Omitted: unchanged. Empty string: remove the password. */
  password?: string
}

// Paths are relative, so the app works under any path prefix.
async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) {
    let message = res.statusText
    try {
      message = (await res.json()).error ?? message
    } catch {
      // Not a JSON error body; keep the status text.
    }
    throw new Error(message)
  }
  if (res.status === 204) return undefined as T
  return res.json() as Promise<T>
}

const q = (params: Record<string, string | number | undefined>) => {
  const s = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) if (v !== undefined) s.set(k, String(v))
  return s.toString()
}

export const api = {
  status: () => request<StatusResponse>('GET', 'api/status'),
  library: () => request<MediaSummary[]>('GET', 'api/library'),
  media: (id: number) => request<MediaDetail>('GET', `api/media/${id}`),
  addMedia: (kind: Kind, tmdbId: number, wantAll: boolean) =>
    request<{ id: number }>('POST', 'api/media', { kind, tmdbId, wantAll }),
  removeMedia: (id: number) => request<void>('DELETE', `api/media/${id}`),
  wantSeason: (id: number, season: number, want: boolean) =>
    request<void>('POST', `api/media/${id}/seasons/${season}`, { want }),
  wantItem: (id: number, want: boolean) => request<void>('POST', `api/items/${id}`, { want }),
  find: (kind: Kind, query: string) => request<FindResult[]>('GET', `api/find?${q({ kind, q: query })}`),
  releases: (media: number, season?: number, episode?: number) =>
    request<Releases>('GET', `api/releases?${q({ media, season, episode })}`),
  searchNow: () => request<void>('POST', 'api/search'),
  failDownload: (id: number) => request<void>('POST', `api/downloads/${id}/fail`),
  settings: () => request<Settings>('GET', 'api/settings'),
  saveSettings: (s: SettingsUpdate) => request<void>('PUT', 'api/settings', s),
  testIndexer: (ix: Indexer) => request<{ ok: boolean; error?: string }>('POST', 'api/indexers/test', ix),
}
