const MAX_INT32 = 2147483647

function parseWholeNumber(raw: string): number | null {
  const text = raw.trim()
  if (!/^\d+$/.test(text)) return null
  const value = Number(text)
  return value <= MAX_INT32 ? value : null
}

export function parseQty(raw: string): number | null {
  const value = parseWholeNumber(raw)
  return value !== null && value > 0 ? value : null
}

export const parsePrice = parseWholeNumber

export const QTY_ERROR = 'Quantity must be a whole number greater than 0'

export interface ProductFormValues {
  sku: string
  name: string
  price: string
}

export type ProductFormErrors = Partial<Record<keyof ProductFormValues, string>>

export function validateProductForm(values: ProductFormValues): ProductFormErrors {
  const errors: ProductFormErrors = {}
  if (!values.sku.trim()) errors.sku = 'SKU is required'
  if (!values.name.trim()) errors.name = 'Name is required'
  if (parsePrice(values.price) === null) errors.price = 'Price must be a whole number of 0 or more'
  return errors
}

export interface OrderLineValues {
  sku: string
  qty: string
}

export interface OrderFormErrors {
  customer?: string
  items?: string
  lines: { sku?: string; qty?: string }[]
}

export function validateOrderForm(customer: string, lines: OrderLineValues[]): OrderFormErrors {
  const errors: OrderFormErrors = { lines: lines.map(() => ({})) }
  if (!customer.trim()) errors.customer = 'Customer name is required'
  if (lines.length === 0) errors.items = 'Add at least one item'

  const seen = new Set<string>()
  lines.forEach((line, index) => {
    const sku = line.sku.trim()
    if (!sku) errors.lines[index].sku = 'SKU is required'
    else if (seen.has(sku)) errors.lines[index].sku = 'This SKU is already in the order'
    seen.add(sku)
    if (parseQty(line.qty) === null) errors.lines[index].qty = QTY_ERROR
  })
  return errors
}

export function hasOrderErrors(errors: OrderFormErrors): boolean {
  return Boolean(errors.customer || errors.items || errors.lines.some((line) => line.sku || line.qty))
}

export function lineIndexFromField(field: string): number | null {
  const match = /^items\[(\d+)\]/.exec(field)
  return match ? Number(match[1]) : null
}
