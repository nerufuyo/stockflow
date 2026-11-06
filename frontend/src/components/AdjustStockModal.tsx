import { useState, type FormEvent } from 'react'
import { api, toApiError, type ApiError } from '../api/client'
import type { MovementType, Product } from '../api/types'
import { parseQty, validateQty } from '../lib/validation'
import { FormError, Modal } from './common'

export function AdjustStockModal({
  product,
  onSaved,
  onStale,
  onClose,
}: {
  product: Product
  onSaved: () => void
  onStale: () => void // the list behind the dialog is out of date
  onClose: () => void
}) {
  const [type, setType] = useState<MovementType>('in')
  const [qty, setQty] = useState('')
  const [note, setNote] = useState('')
  const [qtyError, setQtyError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<ApiError | null>(null)

  async function submit(event: FormEvent) {
    event.preventDefault()
    const invalid = validateQty(qty)
    setQtyError(invalid)
    const parsed = parseQty(qty)
    if (invalid || parsed === null) return

    setSaving(true)
    setError(null)
    try {
      await api.adjustStock(product.id, { type, qty: parsed, note })
      onSaved()
    } catch (err) {
      const apiError = toApiError(err)
      setError(apiError)
      setSaving(false)
      if (apiError.code === 'INSUFFICIENT_STOCK') onStale()
    }
  }

  return (
    <Modal title={`Adjust stock: ${product.sku}`} busy={saving} onClose={onClose}>
      <p className="muted">
        {product.name} · {product.stock} in stock when opened
      </p>
      <form onSubmit={submit} noValidate>
        <fieldset className="segmented">
          <legend>Direction</legend>
          <label>
            <input type="radio" name="type" checked={type === 'in'} onChange={() => setType('in')} /> Stock in
          </label>
          <label>
            <input type="radio" name="type" checked={type === 'out'} onChange={() => setType('out')} /> Stock out
          </label>
        </fieldset>
        <label className="field">
          Quantity
          <input
            inputMode="numeric"
            autoFocus
            value={qty}
            aria-invalid={qtyError !== null}
            onChange={(event) => setQty(event.target.value)}
          />
          {qtyError && <span className="field-error">{qtyError}</span>}
        </label>
        <label className="field">
          Note
          <input value={note} onChange={(event) => setNote(event.target.value)} />
        </label>
        {error?.code === 'INSUFFICIENT_STOCK' ? (
          <div className="alert" role="alert">
            Insufficient stock: only <strong>{error.available}</strong> available.
          </div>
        ) : (
          <FormError error={error} />
        )}
        <div className="modal-actions">
          <button type="button" onClick={onClose} disabled={saving}>
            Close
          </button>
          <button type="submit" className="primary" disabled={saving}>
            {saving ? 'Saving…' : 'Save'}
          </button>
        </div>
      </form>
    </Modal>
  )
}
