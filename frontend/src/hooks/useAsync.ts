import { useCallback, useEffect, useEffectEvent, useState } from 'react'
import { toApiError, type ApiError } from '../api/client'

interface Settled<T> {
  key: string
  attempt: number
  data?: T
  error?: ApiError
}

export function useAsync<T>(load: (signal: AbortSignal) => Promise<T>, deps: readonly unknown[]) {
  const key = JSON.stringify(deps)
  const [attempt, setAttempt] = useState(0)
  const [settled, setSettled] = useState<Settled<T>>()
  const run = useEffectEvent(load)

  useEffect(() => {
    const controller = new AbortController()
    const settle = (result: { data?: T; error?: ApiError }) => {
      if (!controller.signal.aborted) setSettled({ key, attempt, ...result })
    }
    run(controller.signal).then(
      (data) => settle({ data }),
      (err: unknown) => settle({ error: toApiError(err) }),
    )
    return () => controller.abort()
  }, [key, attempt])

  const reload = useCallback(() => setAttempt((n) => n + 1), [])
  const loading = settled?.key !== key || settled.attempt !== attempt
  return { data: settled?.data, error: loading ? undefined : settled.error, loading, reload }
}
