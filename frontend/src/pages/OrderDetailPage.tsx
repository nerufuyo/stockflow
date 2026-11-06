import { useState } from 'react'
import { api } from '../api/client'
import { ErrorState, Loading, OrderStatusBadge } from '../components/common'
import { ConfirmModal } from '../components/ConfirmModal'
import { useAsync } from '../hooks/useAsync'
import { formatDateTime, formatRupiah } from '../lib/format'

export function OrderDetailPage({
  id,
  justCreated,
  onMutated,
}: {
  id: number
  justCreated: boolean
  onMutated: () => void
}) {
  const [confirming, setConfirming] = useState(false)
  const [justCancelled, setJustCancelled] = useState(false)
  const order = useAsync((signal) => api.getOrder(id, signal), [id])

  if (order.error) {
    return (
      <>
        <a href="#/orders">← Orders</a>
        <ErrorState error={order.error} onRetry={order.reload} />
      </>
    )
  }
  if (!order.data) return <Loading />
  const { customer, status, total, created_at, cancelled_at, items } = order.data

  return (
    <>
      <a href="#/orders">← Orders</a>
      {justCreated && status === 'CREATED' && (
        <div className="notice" role="status">
          Order created. The total below was calculated by the server.
        </div>
      )}
      {justCancelled && (
        <div className="notice" role="status">
          Order cancelled and stock restored. <a href="#/products">See the products list</a>.
        </div>
      )}
      <div className="toolbar">
        <h1>Order #{id}</h1>
        <OrderStatusBadge status={status} />
        {status === 'CREATED' && (
          <button type="button" className="danger push-right" onClick={() => setConfirming(true)}>
            Cancel order
          </button>
        )}
      </div>
      <dl className="facts">
        <div>
          <dt>Customer</dt>
          <dd>{customer}</dd>
        </div>
        <div>
          <dt>Created</dt>
          <dd>{formatDateTime(created_at)}</dd>
        </div>
        {cancelled_at && (
          <div>
            <dt>Cancelled</dt>
            <dd>{formatDateTime(cancelled_at)}</dd>
          </div>
        )}
      </dl>
      <div className="table-scroll">
        <table>
          <thead>
            <tr>
              <th>SKU</th>
              <th className="number">Qty</th>
              <th className="number">Unit price</th>
              <th className="number">Line total</th>
            </tr>
          </thead>
          <tbody>
            {items.map((item) => (
              <tr key={item.id}>
                <td className="mono">{item.sku}</td>
                <td className="number">{item.qty}</td>
                <td className="number">{formatRupiah(item.unit_price)}</td>
                <td className="number">{formatRupiah(item.qty * item.unit_price)}</td>
              </tr>
            ))}
          </tbody>
          <tfoot>
            <tr>
              <th colSpan={3} className="number">
                Total
              </th>
              <td className="number total">{formatRupiah(total)}</td>
            </tr>
          </tfoot>
        </table>
      </div>

      {confirming && (
        <ConfirmModal
          title={`Cancel order #${id}?`}
          confirmLabel="Cancel order"
          onConfirm={() => api.cancelOrder(id)}
          onDone={() => {
            setConfirming(false)
            setJustCancelled(true)
            order.reload()
            onMutated()
          }}
          onFailed={() => {
            order.reload()
            onMutated()
          }}
          onClose={() => setConfirming(false)}
        >
          This returns all items to stock. It cannot be undone.
        </ConfirmModal>
      )}
    </>
  )
}
