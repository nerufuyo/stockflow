import { useState, type FormEvent } from 'react'
import { api, toApiError, type ApiError } from '../api/client'
import type { Product } from '../api/types'
import { parsePrice, validateProductForm, type ProductFormErrors } from '../lib/validation'
import { FormError, Modal } from './common'

export function ProductFormModal({
  product,
  onSaved,
  onClose,
}: {
  product: Product | null // null creates a new product
  onSaved: () => void
  onClose: () => void
}) {
  const [sku, setSku] = useState(product?.sku ?? '')
  const [name, setName] = useState(product?.name ?? '')
  const [price, setPrice] = useState(product ? String(product.price) : '')
  const [isActive, setIsActive] = useState(product?.is_active ?? true)
  const [errors, setErrors] = useState<ProductFormErrors>({})
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<ApiError | null>(null)

  async function submit(event: FormEvent) {
    event.preventDefault()
    const invalid = validateProductForm({ sku, name, price })
    setErrors(invalid)
    const parsedPrice = parsePrice(price)
    if (Object.keys(invalid).length > 0 || parsedPrice === null) return

    setSaving(true)
    setError(null)
    const input = { sku: sku.trim(), name: name.trim(), price: parsedPrice, is_active: isActive }
    try {
      await (product ? api.updateProduct(product.id, input) : api.createProduct(input))
      onSaved()
    } catch (err) {
      setError(toApiError(err))
      setSaving(false)
    }
  }

  return (
    <Modal title={product ? `Edit ${product.sku}` : 'New product'} busy={saving} onClose={onClose}>
      <form onSubmit={submit} noValidate>
        <label className="field">
          SKU
          <input value={sku} autoFocus aria-invalid={Boolean(errors.sku)} onChange={(e) => setSku(e.target.value)} />
          {errors.sku && <span className="field-error">{errors.sku}</span>}
        </label>
        <label className="field">
          Name
          <input value={name} aria-invalid={Boolean(errors.name)} onChange={(e) => setName(e.target.value)} />
          {errors.name && <span className="field-error">{errors.name}</span>}
        </label>
        <label className="field">
          Price (Rp)
          <input
            inputMode="numeric"
            value={price}
            aria-invalid={Boolean(errors.price)}
            onChange={(e) => setPrice(e.target.value)}
          />
          {errors.price && <span className="field-error">{errors.price}</span>}
        </label>
        <label className="checkbox">
          <input type="checkbox" checked={isActive} onChange={(e) => setIsActive(e.target.checked)} /> Active
        </label>
        <FormError error={error} />
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
