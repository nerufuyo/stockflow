import { useState } from 'react'
import { api } from '../api/client'
import { EmptyState, ErrorState, Pagination, SkeletonRows } from '../components/common'
import { useAsync } from '../hooks/useAsync'
import { formatDateTime, formatRupiah } from '../lib/format'
import { OrderStatusBadge } from './OrderDetailPage'

const PAGE_SIZE = 10

export function OrdersPage({ version }: { version: number }) {
  const [page, setPage] = useState(1)
  const orders = useAsync((signal) => api.listOrders({ page, limit: PAGE_SIZE }, signal), [page, version])
  const rows = orders.data?.data

  return (
    <>
      <div className="toolbar">
        <h1>Sales orders</h1>
        <a className="button primary push-right" href="#/orders/new">
          New order
        </a>
      </div>
      {orders.error ? (
        <ErrorState error={orders.error} onRetry={orders.reload} />
      ) : rows?.length === 0 ? (
        <EmptyState>No orders yet.</EmptyState>
      ) : (
        <>
          <div className="table-scroll">
            <table className={orders.loading ? 'refreshing' : undefined}>
              <thead>
                <tr>
                  <th>Order</th>
                  <th>Customer</th>
                  <th>Created</th>
                  <th>Status</th>
                  <th className="number">Total</th>
                </tr>
              </thead>
              <tbody>
                {!rows && <SkeletonRows rows={5} columns={5} />}
                {rows?.map((order) => (
                  <tr key={order.id}>
                    <td>
                      <a href={`#/orders/${order.id}`}>#{order.id}</a>
                    </td>
                    <td>{order.customer}</td>
                    <td>{formatDateTime(order.created_at)}</td>
                    <td>
                      <OrderStatusBadge status={order.status} />
                    </td>
                    <td className="number">{formatRupiah(order.total)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {orders.data && (
            <Pagination page={page} limit={orders.data.limit} total={orders.data.total} onPage={setPage} />
          )}
        </>
      )}
    </>
  )
}
