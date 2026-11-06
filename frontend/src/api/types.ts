export interface Product {
  id: number
  sku: string
  name: string
  price: number
  stock: number
  is_active: boolean
  created_at: string
  last_movement_at: string | null
}

export type ProductStatus = 'active' | 'inactive' | 'low_stock'

export type ProductQuery = {
  search?: string
  status?: ProductStatus
  page?: number
  limit?: number
}

export interface ProductInput {
  sku: string
  name: string
  price: number
  is_active: boolean
}

export interface ProductSummary {
  total_products: number
  active_products: number
  total_stock_value: number
  low_stock_count: number
}

export type MovementType = 'in' | 'out'

export interface StockAdjustment {
  type: MovementType
  qty: number
  note: string
}

export interface Movement {
  id: number
  product_id: number
  type: MovementType
  qty: number
  note: string | null
  created_at: string
}

export type OrderStatus = 'CREATED' | 'CANCELLED'

export interface OrderItem {
  id: number
  sku: string
  qty: number
  unit_price: number
}

export interface Order {
  id: number
  customer: string
  status: OrderStatus
  total: number
  created_at: string
  cancelled_at: string | null
  items: OrderItem[]
}

export interface OrderSummary {
  id: number
  customer: string
  status: OrderStatus
  total: number
  created_at: string
}

export interface NewOrder {
  customer: string
  items: { sku: string; qty: number }[]
}

export interface DailySales {
  date: string
  orders: number
  total: number
}

export interface Page<T> {
  data: T[]
  page: number
  limit: number
  total: number
}

export interface FieldError {
  field: string
  message: string
}
