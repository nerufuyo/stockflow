import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, api, parseErrorResponse } from './client'

describe('parseErrorResponse', () => {
  it('keeps the SKU and available quantity of a stock conflict', () => {
    const error = parseErrorResponse(409, {
      error: { code: 'INSUFFICIENT_STOCK', message: 'no', sku: 'A', available: 0 },
    })
    expect(error).toMatchObject({ status: 409, code: 'INSUFFICIENT_STOCK', sku: 'A', available: 0 })
  })

  it('keeps validation details and drops malformed ones', () => {
    const error = parseErrorResponse(400, {
      error: { code: 'VALIDATION_ERROR', message: 'm', details: [{ field: 'sku', message: 'bad' }, { nope: 1 }] },
    })
    expect(error.details).toEqual([{ field: 'sku', message: 'bad' }])
  })

  it.each([null, 'Bad Gateway', {}, { error: 'x' }, { error: {} }])('survives the body %j', (payload) => {
    const error = parseErrorResponse(502, payload)
    expect(error.code).toBe('UNEXPECTED_RESPONSE')
    expect(error.message).toContain('502')
  })
})

describe('request', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('throws ApiError from the envelope', async () => {
    vi.stubGlobal('fetch', async () =>
      new Response(JSON.stringify({ error: { code: 'INSUFFICIENT_STOCK', message: 'm', available: 3 } }), { status: 409 }),
    )
    await expect(api.adjustStock(1, { type: 'out', qty: 9, note: '' })).rejects.toMatchObject({
      code: 'INSUFFICIENT_STOCK',
      available: 3,
    })
  })

  it('reports a network failure as an ApiError', async () => {
    vi.stubGlobal('fetch', async () => {
      throw new TypeError('Failed to fetch')
    })
    const error = await api.productSummary().catch((err: unknown) => err)
    expect(error).toBeInstanceOf(ApiError)
    expect(error).toMatchObject({ code: 'NETWORK_ERROR' })
  })

  it('handles an empty 500 from a dead proxy target', async () => {
    vi.stubGlobal('fetch', async () => new Response('', { status: 500 }))
    await expect(api.productSummary()).rejects.toMatchObject({ code: 'UNEXPECTED_RESPONSE', status: 500 })
  })

  it('returns nothing for 204', async () => {
    vi.stubGlobal('fetch', async () => new Response(null, { status: 204 }))
    await expect(api.deleteProduct(1)).resolves.toBeUndefined()
  })

  it('builds URLs and omits empty query values', async () => {
    const fetchMock = vi.fn(async (_url: string) => new Response('{"data":[]}', { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    await api.listProducts({ search: '', status: 'low_stock', page: 2 })
    expect(fetchMock.mock.calls[0][0]).toBe('/api/products?status=low_stock&page=2')
  })

  it('sends the idempotency key with an order', async () => {
    const fetchMock = vi.fn(async (_url: string, _init: RequestInit) => new Response('{}', { status: 201 }))
    vi.stubGlobal('fetch', fetchMock)
    await api.createOrder({ customer: 'Ann', items: [] }, 'key-1')
    expect(fetchMock.mock.calls[0][1].headers).toMatchObject({ 'Idempotency-Key': 'key-1' })
  })
})
