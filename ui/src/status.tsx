import { createContext, useContext } from 'react'
import type { Progress, StatusResponse } from './api'

// The server status (downloads, VPN, log), polled once in App and shared.
export const StatusContext = createContext<StatusResponse | undefined>(undefined)

export const useStatus = () => useContext(StatusContext)

/** Active downloads per media id. */
export function useActiveByMedia(): Map<number, Progress[]> {
  const status = useStatus()
  const map = new Map<number, Progress[]>()
  for (const p of status?.status.active ?? []) {
    map.set(p.mediaId, [...(map.get(p.mediaId) ?? []), p])
  }
  return map
}
