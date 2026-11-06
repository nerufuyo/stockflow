import { useSyncExternalStore } from 'react'

// ponytail: four routes fit a hash switch. Add react-router when routes need nesting or loaders.
export type Route =
  | { name: 'products' }
  | { name: 'orders' }
  | { name: 'new-order' }
  | { name: 'order'; id: number; justCreated: boolean }
  | { name: 'not-found' }

export function parseRoute(hash: string): Route {
  const [path, query] = hash.replace(/^#/, '').split('?')
  if (path === '' || path === '/' || path === '/products') return { name: 'products' }
  if (path === '/orders') return { name: 'orders' }
  if (path === '/orders/new') return { name: 'new-order' }
  const order = /^\/orders\/(\d+)$/.exec(path)
  if (order) return { name: 'order', id: Number(order[1]), justCreated: query === 'created' }
  return { name: 'not-found' }
}

function subscribe(listener: () => void): () => void {
  window.addEventListener('hashchange', listener)
  return () => window.removeEventListener('hashchange', listener)
}

export function useRoute(): Route {
  return parseRoute(useSyncExternalStore(subscribe, () => window.location.hash))
}
