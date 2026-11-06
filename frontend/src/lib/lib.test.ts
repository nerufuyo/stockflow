import { describe, expect, it } from 'vitest'
import { formatRupiah, timeAgo } from './format'
import { parseRoute } from './route'
import {
  hasOrderErrors,
  lineIndexFromField,
  parsePrice,
  parseQty,
  validateOrderForm,
  validateProductForm,
} from './validation'

describe('parseQty', () => {
  it.each([
    ['1', 1],
    ['  42 ', 42],
    ['2147483647', 2147483647],
  ])('accepts %j', (raw, expected) => expect(parseQty(raw)).toBe(expected))

  it.each(['', ' ', '0', '-1', '1.5', '1e3', 'abc', '2147483648', '٣'])('rejects %j', (raw) =>
    expect(parseQty(raw)).toBeNull(),
  )
})

describe('parsePrice', () => {
  it('allows zero', () => expect(parsePrice('0')).toBe(0))
  it.each(['', '-1', '12.5', '1.000'])('rejects %j', (raw) => expect(parsePrice(raw)).toBeNull())
})

describe('validateProductForm', () => {
  it('passes a valid product', () => {
    expect(validateProductForm({ sku: 'A', name: 'a', price: '0' })).toEqual({})
  })
  it('reports every invalid field', () => {
    expect(Object.keys(validateProductForm({ sku: ' ', name: '', price: 'x' })).sort()).toEqual(['name', 'price', 'sku'])
  })
})

describe('validateOrderForm', () => {
  it('passes a valid order', () => {
    const errors = validateOrderForm('Ann', [
      { sku: 'A', qty: '1' },
      { sku: 'B', qty: '2' },
    ])
    expect(hasOrderErrors(errors)).toBe(false)
  })

  it('requires a customer and at least one item', () => {
    const errors = validateOrderForm(' ', [])
    expect(errors.customer).toBeTruthy()
    expect(errors.items).toBeTruthy()
    expect(hasOrderErrors(errors)).toBe(true)
  })

  it('flags the line that repeats a SKU, ignoring whitespace', () => {
    const errors = validateOrderForm('Ann', [
      { sku: 'A', qty: '1' },
      { sku: ' A ', qty: '1' },
    ])
    expect(errors.lines[0].sku).toBeUndefined()
    expect(errors.lines[1].sku).toMatch(/already/)
  })

  it('flags a blank SKU and a bad quantity on their own line', () => {
    const errors = validateOrderForm('Ann', [
      { sku: 'A', qty: '1' },
      { sku: '', qty: '0' },
    ])
    expect(errors.lines[0]).toEqual({})
    expect(errors.lines[1].sku).toBeTruthy()
    expect(errors.lines[1].qty).toBeTruthy()
  })
})

describe('lineIndexFromField', () => {
  it('reads the line from an API field', () => {
    expect(lineIndexFromField('items[2].qty')).toBe(2)
    expect(lineIndexFromField('customer')).toBeNull()
    expect(lineIndexFromField('items')).toBeNull()
  })
})

describe('formatRupiah', () => {
  it('uses dots as thousands separators', () => {
    expect(formatRupiah(1234567)).toBe('Rp 1.234.567')
    expect(formatRupiah(0)).toBe('Rp 0')
    expect(formatRupiah(10_000_000_000)).toBe('Rp 10.000.000.000')
  })
})

describe('timeAgo', () => {
  const now = Date.parse('2026-01-10T12:00:00Z')
  const ago = (seconds: number) => new Date(now - seconds * 1000).toISOString()

  it.each([
    [0, 'just now'],
    [59, 'just now'],
    [-30, 'just now'],
    [60, '1m ago'],
    [3599, '59m ago'],
    [7200, '2h ago'],
    [86400, '1d ago'],
    [2592000, '1mo ago'],
    [31536000, '1y ago'],
  ])('%i seconds ago is %s', (seconds, label) => expect(timeAgo(ago(seconds), now)).toBe(label))

  it('handles a product that never moved', () => expect(timeAgo(null, now)).toBe('Never'))
})

describe('parseRoute', () => {
  it.each([
    ['', { name: 'products' }],
    ['#/', { name: 'products' }],
    ['#/products', { name: 'products' }],
    ['#/orders', { name: 'orders' }],
    ['#/orders/new', { name: 'new-order' }],
    ['#/orders/7', { name: 'order', id: 7, justCreated: false }],
    ['#/orders/7?created', { name: 'order', id: 7, justCreated: true }],
    ['#/orders/abc', { name: 'not-found' }],
    ['#/nope', { name: 'not-found' }],
  ])('%j', (hash, route) => expect(parseRoute(hash)).toEqual(route))
})
