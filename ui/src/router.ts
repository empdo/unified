import { useSyncExternalStore } from 'react'

// Hash-based routing (#/media/3?season=1). The page itself always stays at
// the app's root URL, so relative API paths and any path prefix keep working.

export interface Route {
  /** Path segments, e.g. ["media", "3"]; [] for the library. */
  parts: string[]
  params: URLSearchParams
}

function subscribe(onChange: () => void) {
  window.addEventListener('hashchange', onChange)
  return () => window.removeEventListener('hashchange', onChange)
}

export function useRoute(): Route {
  const hash = useSyncExternalStore(subscribe, () => window.location.hash)
  const [path, query = ''] = hash.replace(/^#\/?/, '').split('?')
  return { parts: path.split('/').filter(Boolean), params: new URLSearchParams(query) }
}

/** href for a route, for use in links: href("/media/3"). */
export const href = (to: string) => '#' + to

export function navigate(to: string) {
  window.location.hash = to
}
