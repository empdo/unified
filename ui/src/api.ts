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
  /** Bytes per second uploaded to peers. */
  upRate: number
  /** Bytes uploaded over all runs, and that as a multiple of the size. */
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
  transfer: TransferTotals
}

/** Torrent data moved: over all runs, since unified started, and how fast now. */
export interface TransferTotals {
  downloaded: number
  uploaded: number
  sessionDownloaded: number
  sessionUploaded: number
  /** Bytes per second, over all torrents. */
  downRate: number
  upRate: number
}

export type HistoryEventKind =
  | 'added'
  | 'scanned'
  | 'grabbed'
  | 'imported'
  | 'failed'
  | 'cancelled'
  | 'deleted'
  | 'removed'
  | 'seeded'

export interface HistoryEvent {
  id: number
  at: string
  event: HistoryEventKind
  mediaId?: number
  /** What it was about: "Severance S02E03 - Who Is Alive?". */
  title: string
  /** The release, file or reason. */
  detail?: string
  /** The movie or show is still in the library. */
  inLibrary: boolean
}

export interface ScanMatch {
  tmdbId: number
  title: string
  year: number
  poster: string
  libraryId?: number
}

export interface ScanEntry {
  kind: Kind
  path: string
  name: string
  title: string
  year: number
  files: number
  size: number
  /** Shows: episodes told apart by file name, and files whose episode could not be told. */
  episodes: number
  unknown: string[]
  /** Video files the library has already. */
  known: number
  /** TMDB matches, best first. */
  matches: ScanMatch[]
  error?: string
}

export interface ScanResult {
  path: string
  mediaId?: number
  title?: string
  imported: number
  error?: string
}

export type ListKind = 'trakt-watchlist' | 'trakt-list' | 'tmdb-list'
export type ListWant = 'all' | 'first' | 'none'
export type BrowseList = 'trending' | 'popular' | 'top_rated' | 'upcoming'

export interface WatchList {
  id: number
  kind: ListKind
  source: string
  name: string
  want: ListWant
  syncedAt?: string
  lastError?: string
}

export interface SyncResult {
  id: number
  /** Titles added to the library. */
  added: string[]
  error?: string
}

export interface StatusResponse {
  status: Status
  recent: { id: number; name: string; state: string }[]
  tmdb: boolean
  indexers: number
  seedRatio: number
  /** Movies and episodes imported since midnight. */
  arrivedToday: number
}

/** A movie or show that arrived recently, for the library's front page. */
export interface FrontStory extends MediaSummary {
  /** When the newest of it arrived. */
  at: string
  /** What arrived, newest first, as the history names it ("Severance S02E03 - Who Is Alive?"). */
  arrived: string[]
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
  /** When something of it was last imported, if within the last week. */
  arrivedAt?: string
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
  traktClientId: string
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
  traktClientId: string
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
  frontPage: () => request<FrontStory[]>('GET', 'api/frontpage'),
  history: (p: { media?: number; before?: number; events?: string; limit?: number }) =>
    request<{ events: HistoryEvent[]; more: boolean }>('GET', `api/history?${q(p)}`),
  /** Lists what the movie and TV folders hold, with TMDB matches; adds nothing. */
  scan: () => request<{ entries: ScanEntry[]; moviesDir: string; tvDir: string }>('POST', 'api/scan'),
  scanImport: (choices: { kind: Kind; path: string; tmdbId: number }[], wantMissing: boolean) =>
    request<ScanResult[]>('POST', 'api/scan/import', { choices, wantMissing }),
  discover: (kind: Kind, list: BrowseList, page: number) =>
    request<{ results: FindResult[]; pages: number }>('GET', `api/discover?${q({ kind, list, page })}`),
  lists: () => request<{ lists: WatchList[]; trakt: boolean }>('GET', 'api/lists'),
  addList: (kind: ListKind, source: string, want: ListWant) =>
    request<SyncResult>('POST', 'api/lists', { kind, source, want }),
  removeList: (id: number) => request<void>('DELETE', `api/lists/${id}`),
  syncList: (id: number) => request<SyncResult>('POST', `api/lists/${id}/sync`),
}
