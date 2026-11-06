// The only module that talks to the backend: it owns URLs, fetch and the error envelope.
import type {
  DailySales,
  FieldError,
  Movement,
  NewOrder,
  Order,
  OrderSummary,
  Page,
  Product,
  ProductInput,
  ProductQuery,
  ProductSummary,
  StockAdjustment,
} from './types'

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly sku?: string
  readonly available?: number
  readonly details: FieldError[]

  constructor(
    status: number,
    code: string,
    message: string,
    extra: { sku?: string; available?: number; details?: FieldError[] } = {},
  ) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.sku = extra.sku
    this.available = extra.available
    this.details = extra.details ?? []
  }
}

/** Builds an ApiError from any failed response body, including non-JSON ones from a proxy. */
export function parseErrorResponse(status: number, payload: unknown): ApiError {
  const error = isRecord(payload) && isRecord(payload.error) ? payload.error : null
  if (!error || typeof error.code !== 'string') {
    return new ApiError(status, 'UNEXPECTED_RESPONSE', `The server returned an unexpected response (HTTP ${status}).`)
  }
  return new ApiError(status, error.code, typeof error.message === 'string' ? error.message : error.code, {
    sku: typeof error.sku === 'string' ? error.sku : undefined,
    available: typeof error.available === 'number' ? error.available : undefined,
    details: Array.isArray(error.details) ? error.details.filter(isFieldError) : [],
  })
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function isFieldError(value: unknown): value is FieldError {
  return isRecord(value) && typeof value.field === 'string' && typeof value.message === 'string'
}

/** Normalizes anything thrown by a request for display. */
export function toApiError(err: unknown): ApiError {
  return err instanceof ApiError ? err : new ApiError(0, 'UNKNOWN', err instanceof Error ? err.message : 'Unknown error')
}

interface RequestOptions {
  method?: string
  body?: unknown
  signal?: AbortSignal
  headers?: Record<string, string>
}

async function request<T>(path: string, { method = 'GET', body, signal, headers }: RequestOptions = {}): Promise<T> {
  let response: Response
  try {
    response = await fetch(`/api${path}`, {
      method,
      signal,
      headers: { ...(body === undefined ? {} : { 'Content-Type': 'application/json' }), ...headers },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch (err) {
    if (signal?.aborted) throw err
    throw new ApiError(0, 'NETWORK_ERROR', 'Cannot reach the server. Check your connection and try again.')
  }
  if (response.status === 204) return undefined as T

  const payload: unknown = await response.json().catch(() => null)
  if (!response.ok) throw parseErrorResponse(response.status, payload)
  return payload as T
}

function queryString(params: Record<string, string | number | undefined>): string {
  const query = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== '') query.set(key, String(value))
  }
  const text = query.toString()
  return text ? `?${text}` : ''
}

export const api = {
  listProducts: (query: ProductQuery, signal?: AbortSignal) =>
    request<Page<Product>>(`/products${queryString({ ...query })}`, { signal }),
  productSummary: (signal?: AbortSignal) => request<ProductSummary>('/products/summary', { signal }),
  createProduct: (input: ProductInput) => request<Product>('/products', { method: 'POST', body: input }),
  updateProduct: (id: number, input: ProductInput) =>
    request<Product>(`/products/${id}`, { method: 'PUT', body: input }),
  deleteProduct: (id: number) => request<void>(`/products/${id}`, { method: 'DELETE' }),
  adjustStock: (id: number, adjustment: StockAdjustment) =>
    request<Product>(`/products/${id}/stock`, { method: 'POST', body: adjustment }),
  listMovements: (id: number, signal?: AbortSignal) =>
    request<{ data: Movement[] }>(`/products/${id}/movements`, { signal }),

  listOrders: (query: { page: number; limit: number }, signal?: AbortSignal) =>
    request<Page<OrderSummary>>(`/orders${queryString(query)}`, { signal }),
  createOrder: (order: NewOrder, idempotencyKey: string) =>
    request<Order>('/orders', { method: 'POST', body: order, headers: { 'Idempotency-Key': idempotencyKey } }),
  getOrder: (id: number, signal?: AbortSignal) => request<Order>(`/orders/${id}`, { signal }),
  cancelOrder: (id: number) => request<Order>(`/orders/${id}/cancel`, { method: 'POST' }),

  dailySales: (signal?: AbortSignal) => request<DailySales>('/reports/daily-sales', { signal }),
}
