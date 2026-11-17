import { useCallback, useEffect, useRef, useState } from 'react'

export interface Data<T> {
  data: T | undefined
  error: string | undefined
  loading: boolean
  reload: () => void
}

/**
 * Loads data when deps change, and again every pollMs if given. Results of
 * superseded loads are dropped.
 */
export function useData<T>(load: () => Promise<T>, deps: unknown[], pollMs?: number): Data<T> {
  const [data, setData] = useState<T>()
  const [error, setError] = useState<string>()
  const [loading, setLoading] = useState(true)
  const generation = useRef(0)
  const loadRef = useRef(load)
  loadRef.current = load

  const reload = useCallback(() => {
    const gen = ++generation.current
    loadRef.current().then(
      (d) => {
        if (gen !== generation.current) return
        setData(d)
        setError(undefined)
        setLoading(false)
      },
      (e: Error) => {
        if (gen !== generation.current) return
        setError(e.message)
        setLoading(false)
      },
    )
  }, [])

  useEffect(() => {
    setLoading(true)
    setData(undefined)
    reload()
    if (!pollMs) return
    const timer = setInterval(reload, pollMs)
    return () => clearInterval(timer)
  }, [...deps, pollMs])

  return { data, error, loading, reload }
}

/** Runs an action, tracking whether it is busy and its last error. */
export function useAction() {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const run = useCallback(async (action: () => Promise<unknown>) => {
    setBusy(true)
    setError(undefined)
    try {
      await action()
      return true
    } catch (e) {
      setError((e as Error).message)
      return false
    } finally {
      setBusy(false)
    }
  }, [])
  return { busy, error, run }
}
