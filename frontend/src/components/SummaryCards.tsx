import { api } from '../api/client'
import { useAsync } from '../hooks/useAsync'
import { formatRupiah } from '../lib/format'
import { ErrorState } from './common'

export function SummaryCards({ version }: { version: number }) {
  const summary = useAsync(
    (signal) => Promise.all([api.productSummary(signal), api.dailySales(signal)]),
    [version],
  )
  if (summary.error) return <ErrorState error={summary.error} onRetry={summary.reload} />

  const [products, sales] = summary.data ?? []
  const cards = [
    { label: 'Total products', value: products?.total_products },
    { label: 'Active products', value: products?.active_products },
    { label: 'Total stock value', value: products && formatRupiah(products.total_stock_value) },
    { label: 'Low stock (≤ 10)', value: products?.low_stock_count },
    { label: `Sales today (${sales?.orders ?? 0} ${sales?.orders === 1 ? 'order' : 'orders'})`, value: sales && formatRupiah(sales.total) },
  ]
  return (
    <section className="cards" aria-label="Summary">
      {cards.map((card) => (
        <div className="card" key={card.label}>
          <div className="card-label">{card.label}</div>
          <div className="card-value">{card.value ?? <span className="skeleton" />}</div>
        </div>
      ))}
    </section>
  )
}
