import { useRef, useState, type FormEvent } from 'react'
import { api, toApiError, type ApiError } from '../api/client'
import { FormError } from '../components/common'
import { useAsync } from '../hooks/useAsync'
import { formatRupiah } from '../lib/format'
import {
  hasOrderErrors,
  lineIndexFromField,
  validateOrderForm,
  type OrderFormErrors,
  type OrderLineValues,
} from '../lib/validation'

interface Line extends OrderLineValues {
  id: number
}

const NO_ERRORS: OrderFormErrors = { lines: [] }

function newIdempotencyKey(): string {
  // randomUUID does not exist on plain http outside localhost.
  return crypto.randomUUID?.() ?? `${Date.now()}-${Math.random()}`
}

export function NewOrderPage({ onMutated }: { onMutated: () => void }) {
  const [customer, setCustomer] = useState('')
  const [lines, setLines] = useState<Line[]>([{ id: 0, sku: '', qty: '1' }])
  const nextLineId = useRef(1)
  const [errors, setErrors] = useState<OrderFormErrors>(NO_ERRORS)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<ApiError | null>(null)

  // Same form, same key: resubmitting after a lost response cannot double-order.
  const attempt = useRef({ form: '', key: '' })

  const catalog = useAsync((signal) => api.listProducts({ status: 'active', limit: 100 }, signal), [])
  const productsBySku = new Map(catalog.data?.data.map((product) => [product.sku, product]))

  function editLine(id: number, change: Partial<OrderLineValues>) {
    setLines((current) => current.map((line) => (line.id === id ? { ...line, ...change } : line)))
  }

  async function submit(event: FormEvent) {
    event.preventDefault()
    const invalid = validateOrderForm(customer, lines)
    setErrors(invalid)
    if (hasOrderErrors(invalid)) return

    setSaving(true)
    setError(null)
    const payload = {
      customer: customer.trim(),
      items: lines.map((line) => ({ sku: line.sku.trim(), qty: Number(line.qty) })),
    }
    const form = JSON.stringify(payload)
    if (attempt.current.form !== form) attempt.current = { form, key: newIdempotencyKey() }
    try {
      const order = await api.createOrder(payload, attempt.current.key)
      onMutated()
      window.location.hash = `#/orders/${order.id}?created`
    } catch (err) {
      const apiError = toApiError(err)
      setError(apiError)
      setSaving(false)
      if (apiError.code === 'INSUFFICIENT_STOCK') catalog.reload()
    }
  }

  const insufficient = error?.code === 'INSUFFICIENT_STOCK' ? error : null
  const serverLines = new Set(error?.details.map((detail) => lineIndexFromField(detail.field)))

  return (
    <>
      <a href="#/orders">← Orders</a>
      <h1>New sales order</h1>
      <form onSubmit={submit} noValidate className="order-form">
        <label className="field">
          Customer name
          <input
            value={customer}
            autoFocus
            aria-invalid={Boolean(errors.customer)}
            onChange={(event) => setCustomer(event.target.value)}
          />
          {errors.customer && <span className="field-error">{errors.customer}</span>}
        </label>

        <datalist id="product-skus">
          {catalog.data?.data.map((product) => (
            <option key={product.id} value={product.sku}>
              {product.name}
            </option>
          ))}
        </datalist>

        <h2>Items</h2>
        {errors.items && <p className="field-error">{errors.items}</p>}
        {lines.map((line, index) => {
          const product = productsBySku.get(line.sku.trim())
          const failed = insufficient?.sku === line.sku.trim() || serverLines.has(index)
          return (
            <div key={line.id} className={`order-line ${failed ? 'order-line-failed' : ''}`}>
              <label className="field">
                SKU
                <input
                  list="product-skus"
                  value={line.sku}
                  aria-invalid={Boolean(errors.lines[index]?.sku) || failed}
                  onChange={(event) => editLine(line.id, { sku: event.target.value })}
                />
                {errors.lines[index]?.sku && <span className="field-error">{errors.lines[index].sku}</span>}
              </label>
              <label className="field">
                Qty
                <input
                  inputMode="numeric"
                  value={line.qty}
                  aria-invalid={Boolean(errors.lines[index]?.qty)}
                  onChange={(event) => editLine(line.id, { qty: event.target.value })}
                />
                {errors.lines[index]?.qty && <span className="field-error">{errors.lines[index].qty}</span>}
              </label>
              <div className="line-info">
                {product && (
                  <>
                    {product.name}
                    <br />
                    <span className="muted">
                      {formatRupiah(product.price)} · {product.stock} in stock
                    </span>
                  </>
                )}
                {insufficient?.sku === line.sku.trim() && (
                  <div className="field-error" role="alert">
                    Insufficient stock for {insufficient.sku}: only {insufficient.available} available
                  </div>
                )}
              </div>
              <button
                type="button"
                aria-label={`Remove item ${index + 1}`}
                disabled={lines.length === 1}
                onClick={() => {
                  setErrors(NO_ERRORS)
                  setError(null)
                  setLines((current) => current.filter((other) => other.id !== line.id))
                }}
              >
                Remove
              </button>
            </div>
          )
        })}
        <button
          type="button"
          onClick={() => setLines((current) => [...current, { id: nextLineId.current++, sku: '', qty: '1' }])}
        >
          Add item
        </button>

        {!insufficient && <FormError error={error} />}
        {catalog.error && <p className="muted">SKU suggestions are unavailable; you can still type a SKU.</p>}

        <div className="form-actions">
          <button type="submit" className="primary" disabled={saving}>
            {saving ? 'Creating…' : 'Create order'}
          </button>
        </div>
      </form>
    </>
  )
}
