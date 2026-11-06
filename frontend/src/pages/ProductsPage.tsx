import { useState } from 'react'
import { api } from '../api/client'
import type { Product, ProductStatus } from '../api/types'
import { AdjustStockModal } from '../components/AdjustStockModal'
import { EmptyState, ErrorState, Pagination, SkeletonRows } from '../components/common'
import { ConfirmModal } from '../components/ConfirmModal'
import { MovementsModal } from '../components/MovementsModal'
import { ProductFormModal } from '../components/ProductFormModal'
import { useAsync } from '../hooks/useAsync'
import { useDebounce } from '../hooks/useDebounce'
import { formatDateTime, formatRupiah, timeAgo } from '../lib/format'

const PAGE_SIZE = 10
const LOW_STOCK = 10

const FILTERS: { label: string; value: ProductStatus | undefined }[] = [
  { label: 'All', value: undefined },
  { label: 'Active', value: 'active' },
  { label: 'Inactive', value: 'inactive' },
  { label: 'Low stock (≤ 10)', value: 'low_stock' },
]

type Dialog =
  | { kind: 'create' }
  | { kind: 'edit' | 'adjust' | 'history' | 'delete'; product: Product }
  | null

export function ProductsPage({ version, onMutated }: { version: number; onMutated: () => void }) {
  const [search, setSearch] = useState('')
  const [status, setStatus] = useState<ProductStatus | undefined>(undefined)
  const [page, setPage] = useState(1)
  const [dialog, setDialog] = useState<Dialog>(null)
  const debouncedSearch = useDebounce(search, 300)

  const products = useAsync(
    (signal) => api.listProducts({ search: debouncedSearch, status, page, limit: PAGE_SIZE }, signal),
    [debouncedSearch, status, page, version],
  )
  const rows = products.data?.data

  const saved = () => {
    setDialog(null)
    onMutated()
  }

  return (
    <>
      <div className="toolbar">
        <input
          type="search"
          placeholder="Search SKU or name"
          aria-label="Search products"
          value={search}
          onChange={(event) => {
            setSearch(event.target.value)
            setPage(1)
          }}
        />
        <div className="filters" role="group" aria-label="Filter products">
          {FILTERS.map((filter) => (
            <button
              type="button"
              key={filter.label}
              aria-pressed={status === filter.value}
              onClick={() => {
                setStatus(filter.value)
                setPage(1)
              }}
            >
              {filter.label}
            </button>
          ))}
        </div>
        <button type="button" className="primary push-right" onClick={() => setDialog({ kind: 'create' })}>
          New product
        </button>
      </div>

      {products.error ? (
        <ErrorState error={products.error} onRetry={products.reload} />
      ) : rows?.length === 0 ? (
        <EmptyState>
          {debouncedSearch || status ? 'No products match your search or filter.' : 'No products yet.'}
        </EmptyState>
      ) : (
        <>
          <div className="table-scroll">
            <table className={products.loading ? 'refreshing' : undefined}>
              <thead>
                <tr>
                  <th>SKU</th>
                  <th>Name</th>
                  <th className="number">Price</th>
                  <th className="number">Stock</th>
                  <th>Status</th>
                  <th>Last movement</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {!rows && <SkeletonRows rows={PAGE_SIZE} columns={7} />}
                {rows?.map((product) => (
                  <tr key={product.id}>
                    <td className="mono">{product.sku}</td>
                    <td>{product.name}</td>
                    <td className="number">{formatRupiah(product.price)}</td>
                    <td className={`number ${product.stock <= LOW_STOCK ? 'low-stock' : ''}`}>{product.stock}</td>
                    <td>
                      <span className={`badge ${product.is_active ? 'badge-ok' : 'badge-muted'}`}>
                        {product.is_active ? 'Active' : 'Inactive'}
                      </span>
                    </td>
                    <td title={product.last_movement_at ? formatDateTime(product.last_movement_at) : undefined}>
                      {timeAgo(product.last_movement_at)}
                    </td>
                    <td className="actions">
                      <button type="button" onClick={() => setDialog({ kind: 'adjust', product })}>
                        Adjust
                      </button>
                      <button type="button" onClick={() => setDialog({ kind: 'history', product })}>
                        History
                      </button>
                      <button type="button" onClick={() => setDialog({ kind: 'edit', product })}>
                        Edit
                      </button>
                      <button type="button" onClick={() => setDialog({ kind: 'delete', product })}>
                        Delete
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {products.data && (
            <Pagination page={page} limit={products.data.limit} total={products.data.total} onPage={setPage} />
          )}
        </>
      )}

      {dialog?.kind === 'create' && <ProductFormModal product={null} onSaved={saved} onClose={() => setDialog(null)} />}
      {dialog?.kind === 'edit' && (
        <ProductFormModal product={dialog.product} onSaved={saved} onClose={() => setDialog(null)} />
      )}
      {dialog?.kind === 'adjust' && (
        <AdjustStockModal product={dialog.product} onSaved={saved} onStale={onMutated} onClose={() => setDialog(null)} />
      )}
      {dialog?.kind === 'history' && <MovementsModal product={dialog.product} onClose={() => setDialog(null)} />}
      {dialog?.kind === 'delete' && (
        <ConfirmModal
          title={`Delete ${dialog.product.sku}?`}
          confirmLabel="Delete"
          onConfirm={() => api.deleteProduct(dialog.product.id)}
          onDone={() => {
            if (rows?.length === 1 && page > 1) setPage(page - 1)
            saved()
          }}
          onClose={() => setDialog(null)}
        >
          {dialog.product.name} will be removed permanently.
        </ConfirmModal>
      )}
    </>
  )
}
