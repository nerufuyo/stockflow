import { useCallback, useEffect, useState, type DependencyList } from 'react'
import { toApiError, type ApiError } from '../api/client'

interface AsyncState<T> {
  data?: T
  error?: ApiError
  loading: boolean
}

/** Runs a request when deps change. A newer run aborts the older one, so stale responses never land. */
export function useAsync<T>(load: (signal: AbortSignal) => Promise<T>, deps: DependencyList) {
  const [state, setState] = useState<AsyncState<T>>({ loading: true })
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    setState((previous) => ({ data: previous.data, loading: true }))
    load(controller.signal).then(
      (data) => {
        if (!controller.signal.aborted) setState({ data, loading: false })
      },
      (err: unknown) => {
        if (!controller.signal.aborted) setState({ error: toApiError(err), loading: false })
      },
    )
    return () => controller.abort()
    // eslint-disable-next-line react-hooks/exhaustive-deps -- the caller's deps stand in for `load`
  }, [...deps, attempt])

  const reload = useCallback(() => setAttempt((n) => n + 1), [])
  return { ...state, reload }
}
