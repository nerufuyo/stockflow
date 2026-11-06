package store

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type OrderItem struct {
	ID        int    `json:"id"`
	SKU       string `json:"sku"`
	Qty       int    `json:"qty"`
	UnitPrice int    `json:"unit_price"`
}

type Order struct {
	ID          int         `json:"id"`
	Customer    string      `json:"customer"`
	Status      string      `json:"status"`
	Total       int64       `json:"total"`
	CreatedAt   time.Time   `json:"created_at"`
	CancelledAt *time.Time  `json:"cancelled_at"`
	Items       []OrderItem `json:"items"`
}

type OrderSummary struct {
	ID        int       `json:"id"`
	Customer  string    `json:"customer"`
	Status    string    `json:"status"`
	Total     int64     `json:"total"`
	CreatedAt time.Time `json:"created_at"`
}

type OrderLine struct {
	SKU string
	Qty int
}

type NewOrder struct {
	Customer       string
	Lines          []OrderLine
	IdempotencyKey string
}

type DailySales struct {
	Date   string `json:"date"`
	Orders int    `json:"orders"`
	Total  int64  `json:"total"`
}

// CreateOrder runs in one transaction, so any early return leaves nothing behind.
// The bool reports an idempotent replay.
func (s *Store) CreateOrder(ctx context.Context, in NewOrder) (Order, bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Order{}, false, err
	}
	defer tx.Rollback(ctx)

	var key, hash *string
	if in.IdempotencyKey != "" {
		h := requestHash(in)
		key, hash = &in.IdempotencyKey, &h
		order, err := replayOrder(ctx, tx, *key, *hash)
		if !errors.Is(err, pgx.ErrNoRows) {
			return order, err == nil, err
		}
	}

	if err := validateProducts(ctx, tx, in.Lines); err != nil {
		return Order{}, false, err
	}

	// A concurrent retry with the same key blocks here on the unique index until this transaction ends.
	var orderID int
	err = tx.QueryRow(ctx, `
		INSERT INTO sales_orders (customer, total, idempotency_key, request_hash) VALUES ($1, 0, $2, $3)
		ON CONFLICT (idempotency_key) DO NOTHING RETURNING id`, in.Customer, key, hash).Scan(&orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		order, err := replayOrder(ctx, tx, in.IdempotencyKey, *hash)
		return order, true, err
	}
	if err != nil {
		return Order{}, false, err
	}

	// A fixed lock order: two orders sharing SKUs cannot deadlock.
	lines := slices.SortedFunc(slices.Values(in.Lines), func(a, b OrderLine) int { return cmp.Compare(a.SKU, b.SKU) })

	var total int64
	for _, line := range lines {
		var productID, price int
		err := tx.QueryRow(ctx, `
			UPDATE products SET stock = stock - $2 WHERE sku = $1 AND stock >= $2
			RETURNING id, price`, line.SKU, line.Qty).Scan(&productID, &price)
		if errors.Is(err, pgx.ErrNoRows) {
			return Order{}, false, insufficientOrMissing(ctx, tx, line.SKU)
		}
		if err != nil {
			return Order{}, false, err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO sales_order_items (order_id, sku, qty, unit_price) VALUES ($1, $2, $3, $4)`,
			orderID, line.SKU, line.Qty, price); err != nil {
			return Order{}, false, err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO stock_movements (product_id, type, qty, note) VALUES ($1, 'out', $2, $3)`,
			productID, line.Qty, fmt.Sprintf("Order #%d", orderID)); err != nil {
			return Order{}, false, err
		}
		total += int64(price) * int64(line.Qty)
	}

	if _, err := tx.Exec(ctx, `UPDATE sales_orders SET total = $2 WHERE id = $1`, orderID, total); err != nil {
		return Order{}, false, err
	}
	order, err := getOrder(ctx, tx, orderID)
	if err != nil {
		return Order{}, false, err
	}
	return order, false, tx.Commit(ctx)
}

func validateProducts(ctx context.Context, tx pgx.Tx, lines []OrderLine) error {
	skus := make([]string, len(lines))
	for i, line := range lines {
		skus[i] = line.SKU
	}
	rows, err := tx.Query(ctx, `SELECT sku, is_active FROM products WHERE sku = ANY($1)`, skus)
	if err != nil {
		return err
	}
	active := map[string]bool{}
	var sku string
	var isActive bool
	if _, err := pgx.ForEachRow(rows, []any{&sku, &isActive}, func() error {
		active[sku] = isActive
		return nil
	}); err != nil {
		return err
	}

	var details []FieldError
	for i, line := range lines {
		field := fmt.Sprintf("items[%d].sku", i)
		isActive, known := active[line.SKU]
		switch {
		case !known:
			details = append(details, FieldError{field, fmt.Sprintf("unknown SKU %q", line.SKU)})
		case !isActive:
			details = append(details, FieldError{field, fmt.Sprintf("SKU %q is inactive", line.SKU)})
		}
	}
	if details != nil {
		return &ValidationError{details}
	}
	return nil
}

func insufficientOrMissing(ctx context.Context, tx pgx.Tx, sku string) error {
	var available int
	err := tx.QueryRow(ctx, `SELECT stock FROM products WHERE sku = $1`, sku).Scan(&available)
	if errors.Is(err, pgx.ErrNoRows) {
		return &ValidationError{[]FieldError{{"items", fmt.Sprintf("unknown SKU %q", sku)}}}
	}
	if err != nil {
		return err
	}
	return &InsufficientStockError{SKU: sku, Available: available}
}

func replayOrder(ctx context.Context, tx pgx.Tx, key, hash string) (Order, error) {
	var orderID int
	var storedHash string
	err := tx.QueryRow(ctx, `SELECT id, request_hash FROM sales_orders WHERE idempotency_key = $1`, key).
		Scan(&orderID, &storedHash)
	if err != nil {
		return Order{}, err
	}
	if storedHash != hash {
		return Order{}, &ConflictError{"IDEMPOTENCY_KEY_REUSED", "Idempotency-Key was already used with a different request"}
	}
	return getOrder(ctx, tx, orderID)
}

func requestHash(in NewOrder) string {
	lines := make([]string, len(in.Lines))
	for i, line := range in.Lines {
		lines[i] = fmt.Sprintf("%q:%d", line.SKU, line.Qty)
	}
	slices.Sort(lines)
	sum := sha256.Sum256(fmt.Appendf(nil, "%q|%s", in.Customer, strings.Join(lines, ",")))
	return hex.EncodeToString(sum[:])
}

func (s *Store) GetOrder(ctx context.Context, id int) (Order, error) { return getOrder(ctx, s.db, id) }

func getOrder(ctx context.Context, q querier, id int) (Order, error) {
	rows, err := q.Query(ctx, `SELECT id, customer, status, total, created_at, cancelled_at FROM sales_orders WHERE id = $1`, id)
	if err != nil {
		return Order{}, err
	}
	order, err := pgx.CollectExactlyOneRow(rows, func(row pgx.CollectableRow) (Order, error) {
		var o Order
		err := row.Scan(&o.ID, &o.Customer, &o.Status, &o.Total, &o.CreatedAt, &o.CancelledAt)
		return o, err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrNotFound
	}
	if err != nil {
		return Order{}, err
	}

	rows, err = q.Query(ctx, `SELECT id, sku, qty, unit_price FROM sales_order_items WHERE order_id = $1 ORDER BY id`, id)
	if err != nil {
		return Order{}, err
	}
	order.Items, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (OrderItem, error) {
		var it OrderItem
		err := row.Scan(&it.ID, &it.SKU, &it.Qty, &it.UnitPrice)
		return it, err
	})
	return order, err
}

func (s *Store) CancelOrder(ctx context.Context, id int) (Order, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Order{}, err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `UPDATE sales_orders SET status = 'CANCELLED', cancelled_at = now() WHERE id = $1 AND status = 'CREATED'`, id)
	if err != nil {
		return Order{}, err
	}
	if tag.RowsAffected() == 0 {
		if _, err := getOrder(ctx, tx, id); err != nil {
			return Order{}, err
		}
		return Order{}, &ConflictError{"ORDER_ALREADY_CANCELLED", "Order is already cancelled"}
	}

	rows, err := tx.Query(ctx, `SELECT sku, qty FROM sales_order_items WHERE order_id = $1 ORDER BY sku`, id)
	if err != nil {
		return Order{}, err
	}
	lines, err := pgx.CollectRows(rows, pgx.RowToStructByPos[OrderLine])
	if err != nil {
		return Order{}, err
	}
	for _, line := range lines {
		var productID int
		err := tx.QueryRow(ctx, `UPDATE products SET stock = stock + $2 WHERE sku = $1 RETURNING id`, line.SKU, line.Qty).Scan(&productID)
		if err != nil {
			return Order{}, err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO stock_movements (product_id, type, qty, note) VALUES ($1, 'in', $2, $3)`,
			productID, line.Qty, fmt.Sprintf("Order #%d cancelled", id)); err != nil {
			return Order{}, err
		}
	}

	order, err := getOrder(ctx, tx, id)
	if err != nil {
		return Order{}, err
	}
	return order, tx.Commit(ctx)
}

func (s *Store) ListOrders(ctx context.Context, page, limit int) ([]OrderSummary, int, error) {
	var total int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM sales_orders`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, customer, status, total, created_at FROM sales_orders
		ORDER BY id DESC LIMIT $1 OFFSET $2`, limit, (page-1)*limit)
	if err != nil {
		return nil, 0, err
	}
	orders, err := pgx.CollectRows(rows, pgx.RowToStructByPos[OrderSummary])
	return orders, total, err
}

// DailySales reports non-cancelled orders for a date in Asia/Jakarta, or today when date is nil.
func (s *Store) DailySales(ctx context.Context, date *string) (DailySales, error) {
	var d DailySales
	err := s.db.QueryRow(ctx, `
		WITH day AS (SELECT COALESCE($1::DATE, (now() AT TIME ZONE 'Asia/Jakarta')::DATE) AS d)
		SELECT day.d::TEXT, count(o.id), COALESCE(sum(o.total), 0)::BIGINT
		FROM day LEFT JOIN sales_orders o
		  ON (o.created_at AT TIME ZONE 'Asia/Jakarta')::DATE = day.d AND o.status = 'CREATED'
		GROUP BY day.d`, date).Scan(&d.Date, &d.Orders, &d.Total)
	return d, err
}
