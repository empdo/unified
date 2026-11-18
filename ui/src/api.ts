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
  /** Seeding downloads: bytes uploaded since unified started, and as a multiple of the size. */
  uploaded: number
  ratio: number
  /** Peer connections over uTP and TCP. */
  utp: number
  tcp: number
  /** Connected peers with the whole torrent. */
  seeders: number
  /** Peers known of, connected or not. */
  known: number
  /** The largest share of the torrent (0 to 1) any connected peer has. */
  best: number
  /** When it started downloading (not while queued). */
  started?: string
  /** When it is given up on, and another release tried, unless it makes progress. */
  givesUpAt?: string
  /** A queued download's place in line, from 1. */
  position?: number
  /** A seed waiting for a seeding slot. */
  paused?: boolean
}

export interface Volume {
  /** The directories on this filesystem: Downloads, Movies, TV. */
  labels: string[]
  free: number
  total: number
  error?: string
}

export interface DiskStatus {
  volumes: Volume[]
  /** Free space for downloads once the active ones have finished. */
  available: number
  /** Space kept free; 0 when not checked. */
  minFree: number
  /** New downloads are held back for lack of space. */
  low: boolean
  error?: string
}

export interface FolderUsage {
  label: string
  path: string
  /** Bytes the folder takes on disk. */
  size: number
  /** Of size, files hardlinked elsewhere too (stored once). */
  linked: number
  files: number
  /** Top-level entries, largest first. */
  entries: { name: string; size: number }[]
  /** The disk the folder is on. */
  free: number
  total: number
  sameDiskAs: string[]
  error?: string
}

export interface StorageReport {
  folders: FolderUsage[]
  disk: DiskStatus
  measuredAt: string
}

export interface Status {
  active: Progress[]
  seeding: Progress[]
  log: string[]
  vpn: string
  vpnError: string
  disk: DiskStatus
  /** What is being searched for on the indexers, and how many wanted items wait for a search. */
  search: { current: { mediaId: number; label: string } | null; queued: number }
  /** Downloads waiting for a slot, in order. */
  queued: Progress[]
  limits: {
    /** Bytes per second; 0 is no limit. */
    download: number
    upload: number
    /** The alternative limits are in force. */
    alt: boolean
    maxDownloads: number
    maxSeeds: number
  }
}

export interface StatusResponse {
  status: Status
  recent: { id: number; name: string; state: string }[]
  tmdb: boolean
  indexers: number
  seedRatio: number
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
  /** The download bringing it, while it is downloading. */
  downloadId?: number
  /** Where the search for it stands, while it is wanted. */
  search?: { state: 'queued' | 'searching' | 'notFound'; at: string; note?: string }
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
  link: string
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

/** Speed limits in KiB/s and counts; 0 is no limit. */
export interface Transfer {
  maxDownloadKBps: number
  maxUploadKBps: number
  altDownloadKBps: number
  altUploadKBps: number
  /** "08:00-23:00", or empty for none. */
  altSchedule: string
  maxActiveDownloads: number
  maxActiveSeeds: number
  maxConnections: number
}

export interface Settings {
  tmdbKey: string
  indexers: Indexer[]
  quality: Quality
  /** 0 means no limit. */
  seedRatio: number
  /** 0 turns the free space check off. */
  minFreeGB: number
  notifyURL: string
  transfer: Transfer
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
  seedRatio: number
  minFreeGB: number
  notifyURL: string
  transfer: Transfer
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
  /** Removes from the library; with deleteFiles its files are deleted too. */
  removeMedia: (id: number, deleteFiles: boolean) =>
    request<void>('DELETE', `api/media/${id}${deleteFiles ? '?files=1' : ''}`),
  /** Deletes the files but keeps it in the library, no longer wanted. */
  deleteFiles: (id: number) => request<void>('DELETE', `api/media/${id}/files`),
  wantSeason: (id: number, season: number, want: boolean) =>
    request<void>('POST', `api/media/${id}/seasons/${season}`, { want }),
  wantItem: (id: number, want: boolean) => request<void>('POST', `api/items/${id}`, { want }),
  /** Deletes a season's episode files, stopping their downloads, and stops wanting them. */
  deleteSeasonFiles: (id: number, season: number) => request<void>('DELETE', `api/media/${id}/seasons/${season}/files`),
  /** Stops a season's downloads, deleting what they downloaded, and stops wanting its missing episodes. */
  cancelSeason: (id: number, season: number) => request<void>('POST', `api/media/${id}/seasons/${season}/cancel`),
  /** Deletes one episode's file, and stops wanting it. */
  deleteItemFiles: (id: number) => request<void>('DELETE', `api/items/${id}/files`),
  find: (kind: Kind, query: string) => request<FindResult[]>('GET', `api/find?${q({ kind, q: query })}`),
  releases: (media: number, season?: number, episode?: number) =>
    request<Releases>('GET', `api/releases?${q({ media, season, episode })}`),
  /** Episode 0 grabs for a whole season; a movie needs neither. */
  grab: (media: number, season: number, episode: number, c: Candidate) =>
    request<void>('POST', 'api/releases/grab', { media, season, episode, title: c.title, link: c.link }),
  searchNow: () => request<void>('POST', 'api/search'),
  failDownload: (id: number) => request<void>('POST', `api/downloads/${id}/fail`),
  settings: () => request<Settings>('GET', 'api/settings'),
  saveSettings: (s: SettingsUpdate) => request<void>('PUT', 'api/settings', s),
  testIndexer: (ix: Indexer) => request<{ ok: boolean; error?: string }>('POST', 'api/indexers/test', ix),
  storage: (fresh = false) => request<StorageReport>('GET', `api/storage${fresh ? '?fresh=1' : ''}`),
  testNotify: (url: string) => request<{ ok: boolean; error?: string }>('POST', 'api/notify/test', { url }),
}
