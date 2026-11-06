import { useState, type ReactNode } from 'react'
import { toApiError, type ApiError } from '../api/client'
import { FormError, Modal } from './common'

/** A confirmation whose action can fail: the backend's refusal stays visible in the dialog. */
export function ConfirmModal({
  title,
  confirmLabel,
  children,
  onConfirm,
  onDone,
  onFailed,
  onClose,
}: {
  title: string
  confirmLabel: string
  children: ReactNode
  onConfirm: () => Promise<unknown>
  onDone: () => void
  onFailed?: () => void
  onClose: () => void
}) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ApiError | null>(null)

  async function confirm() {
    setBusy(true)
    setError(null)
    try {
      await onConfirm()
      onDone()
    } catch (err) {
      setError(toApiError(err))
      setBusy(false)
      onFailed?.()
    }
  }

  return (
    <Modal title={title} busy={busy} onClose={onClose}>
      <p>{children}</p>
      <FormError error={error} />
      <div className="modal-actions">
        <button type="button" onClick={onClose} disabled={busy}>
          Keep
        </button>
        <button type="button" className="danger" onClick={confirm} disabled={busy}>
          {busy ? 'Working…' : confirmLabel}
        </button>
      </div>
    </Modal>
  )
}
