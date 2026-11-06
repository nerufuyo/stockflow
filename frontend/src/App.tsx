import { useCallback, useState } from 'react'
import { SummaryCards } from './components/SummaryCards'
import { useRoute } from './lib/route'
import { NewOrderPage } from './pages/NewOrderPage'
import { OrderDetailPage } from './pages/OrderDetailPage'
import { OrdersPage } from './pages/OrdersPage'
import { ProductsPage } from './pages/ProductsPage'

export default function App() {
  const route = useRoute()
  const [version, setVersion] = useState(0)
  const onMutated = useCallback(() => setVersion((n) => n + 1), [])

  return (
    <>
      <header className="app-header">
        <strong>StockFlow</strong>
        <nav>
          <a href="#/products" aria-current={route.name === 'products' ? 'page' : undefined}>
            Products
          </a>
          <a href="#/orders" aria-current={route.name !== 'products' ? 'page' : undefined}>
            Orders
          </a>
        </nav>
      </header>
      <main>
        <SummaryCards version={version} />
        {route.name === 'products' && <ProductsPage version={version} onMutated={onMutated} />}
        {route.name === 'orders' && <OrdersPage version={version} />}
        {route.name === 'new-order' && <NewOrderPage onMutated={onMutated} />}
        {route.name === 'order' && (
          <OrderDetailPage key={route.id} id={route.id} justCreated={route.justCreated} onMutated={onMutated} />
        )}
        {route.name === 'not-found' && <p className="state">Page not found.</p>}
      </main>
    </>
  )
}
