import { api } from '../api/client'
import type { Product } from '../api/types'
import { useAsync } from '../hooks/useAsync'
import { formatDateTime } from '../lib/format'
import { EmptyState, ErrorState, Loading, Modal } from './common'

export function MovementsModal({ product, onClose }: { product: Product; onClose: () => void }) {
  const movements = useAsync((signal) => api.listMovements(product.id, signal), [product.id])

  return (
    <Modal title={`Stock history: ${product.sku}`} onClose={onClose}>
      {movements.error ? (
        <ErrorState error={movements.error} onRetry={movements.reload} />
      ) : !movements.data ? (
        <Loading />
      ) : movements.data.data.length === 0 ? (
        <EmptyState>No stock movements yet.</EmptyState>
      ) : (
        <div className="table-scroll history">
          <table>
            <thead>
              <tr>
                <th>When</th>
                <th>Direction</th>
                <th className="number">Qty</th>
                <th>Note</th>
              </tr>
            </thead>
            <tbody>
              {movements.data.data.map((movement) => (
                <tr key={movement.id}>
                  <td>{formatDateTime(movement.created_at)}</td>
                  <td>
                    <span className={`badge ${movement.type === 'in' ? 'badge-ok' : 'badge-muted'}`}>
                      {movement.type === 'in' ? 'In' : 'Out'}
                    </span>
                  </td>
                  <td className="number">{movement.qty}</td>
                  <td>{movement.note ?? '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <div className="modal-actions">
        <button type="button" onClick={onClose}>
          Close
        </button>
      </div>
    </Modal>
  )
}
