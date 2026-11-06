import { useEffect, useRef, type ReactNode } from 'react'
import type { ApiError } from '../api/client'

export function ErrorState({ error, onRetry }: { error: ApiError; onRetry: () => void }) {
  return (
    <div className="state state-error" role="alert">
      <p>{error.message}</p>
      <button type="button" onClick={onRetry}>
        Retry
      </button>
    </div>
  )
}

export function EmptyState({ children }: { children: ReactNode }) {
  return <div className="state">{children}</div>
}

export function SkeletonRows({ rows, columns }: { rows: number; columns: number }) {
  return (
    <>
      {Array.from({ length: rows }, (_, row) => (
        <tr key={row} aria-hidden="true">
          {Array.from({ length: columns }, (_, column) => (
            <td key={column}>
              <span className="skeleton" />
            </td>
          ))}
        </tr>
      ))}
    </>
  )
}

export function Loading() {
  return (
    <div className="state" role="status">
      Loading…
    </div>
  )
}

/** Shows a request failure inside a form, with the API's per-field messages when it sent any. */
export function FormError({ error }: { error: ApiError | null }) {
  if (!error) return null
  return (
    <div className="alert" role="alert">
      {error.details.length > 0 ? (
        <ul>
          {error.details.map((detail) => (
            <li key={`${detail.field}:${detail.message}`}>{detail.message}</li>
          ))}
        </ul>
      ) : (
        error.message
      )}
    </div>
  )
}

export function Pagination({
  page,
  limit,
  total,
  onPage,
}: {
  page: number
  limit: number
  total: number
  onPage: (page: number) => void
}) {
  const pages = Math.max(1, Math.ceil(total / limit))
  return (
    <nav className="pagination" aria-label="Pagination">
      <button type="button" disabled={page <= 1} onClick={() => onPage(page - 1)}>
        Previous
      </button>
      <span>
        Page {page} of {pages} · {total} total
      </span>
      <button type="button" disabled={page >= pages} onClick={() => onPage(page + 1)}>
        Next
      </button>
    </nav>
  )
}

/** Native <dialog>: focus trap, Esc and backdrop come from the browser. */
export function Modal({
  title,
  busy = false,
  onClose,
  children,
}: {
  title: string
  busy?: boolean
  onClose: () => void
  children: ReactNode
}) {
  const ref = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    if (!ref.current?.open) ref.current?.showModal()
  }, [])
  return (
    <dialog
      ref={ref}
      aria-label={title}
      onCancel={(event) => {
        event.preventDefault()
        if (!busy) onClose()
      }}
    >
      <h2>{title}</h2>
      {children}
    </dialog>
  )
}
