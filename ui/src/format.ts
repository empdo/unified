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
