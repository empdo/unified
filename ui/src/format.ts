export function bytes(n: number): string {
  if (n >= 1 << 30) return `${(n / (1 << 30)).toFixed(2)} GB`
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`
  return `${Math.round(n / 1024)} KB`
}

export const rate = (n: number) => (n > 0 ? `${bytes(n)}/s` : '')

export const plural = (n: number, one: string, many = one + 's') => `${n} ${n === 1 ? one : many}`

export const episodeCode = (season: number, episode: number) =>
  `S${String(season).padStart(2, '0')}E${String(episode).padStart(2, '0')}`

const stateLabels: Record<string, string> = {
  ignored: 'Not wanted',
  wanted: 'Wanted',
  downloading: 'Downloading',
  imported: 'In library',
  done: 'Done',
  failed: 'Failed',
}

export const stateLabel = (state: string) => stateLabels[state] ?? state

/** A TMDB air date (2025-01-31) as "31 Jan 2025"; empty when unknown. */
export function airDate(date: string): string {
  const d = new Date(date + 'T00:00:00')
  if (!date || isNaN(d.getTime())) return ''
  return d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' })
}

/** Roughly how long a download has left at its current rate: "17 min left". */
export function timeLeft(done: number, total: number, rate: number): string {
  if (rate <= 0 || total <= done) return ''
  const minutes = Math.ceil((total - done) / rate / 60)
  if (minutes < 60) return `${minutes} min left`
  const h = Math.floor(minutes / 60)
  const m = minutes % 60
  return m ? `${h} h ${m} min left` : `${h} h left`
}
