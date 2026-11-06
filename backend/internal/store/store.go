// Package store owns all SQL. Stock is only ever changed by a guarded UPDATE in the same
// transaction as its movement row, so stock always equals SUM(in) - SUM(out).
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LowStockThreshold is shared by the product filter and the summary count.
const LowStockThreshold = 10

// Postgres error codes the API translates into client errors.
const (
	pgForeignKeyViolation = "23503"
	pgUniqueViolation     = "23505"
	pgNumericOutOfRange   = "22003"
)

// ErrNotFound means the addressed row does not exist.
var ErrNotFound = errors.New("not found")

// FieldError is one client-fixable problem with a request.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidationError is reported to the client as 400.
type ValidationError struct{ Details []FieldError }

func (e *ValidationError) Error() string { return fmt.Sprintf("validation failed: %v", e.Details) }

// InsufficientStockError is reported to the client as 409. SKU is empty for single-product adjustments.
type InsufficientStockError struct {
	SKU       string
	Available int
}

func (e *InsufficientStockError) Error() string {
	return fmt.Sprintf("insufficient stock for %q: %d available", e.SKU, e.Available)
}

// ConflictError is any other 409, identified by its code.
type ConflictError struct{ Code, Message string }

func (e *ConflictError) Error() string { return e.Code + ": " + e.Message }

type Store struct{ db *pgxpool.Pool }

func New(db *pgxpool.Pool) *Store { return &Store{db: db} }

func (s *Store) Ping(ctx context.Context) error { return s.db.Ping(ctx) }

type Product struct {
	ID             int        `json:"id"`
	SKU            string     `json:"sku"`
	Name           string     `json:"name"`
	Price          int        `json:"price"`
	Stock          int        `json:"stock"`
	IsActive       bool       `json:"is_active"`
	CreatedAt      time.Time  `json:"created_at"`
	LastMovementAt *time.Time `json:"last_movement_at"`
}

type Movement struct {
	ID        int       `json:"id"`
	ProductID int       `json:"product_id"`
	Type      string    `json:"type"`
	Qty       int       `json:"qty"`
	Note      *string   `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

type Summary struct {
	TotalProducts  int   `json:"total_products"`
	ActiveProducts int   `json:"active_products"`
	TotalValue     int64 `json:"total_stock_value"`
	LowStockCount  int   `json:"low_stock_count"`
}

// ProductInput is the writable part of a product. Stock is deliberately absent: only movements change it.
type ProductInput struct {
	SKU      string
	Name     string
	Price    int
	IsActive *bool // nil keeps the current value (defaults to true on create)
}

type ProductFilter struct {
	Search string
	Status string // "", "active", "inactive" or "low_stock"
	Page   int
	Limit  int
}

// last_movement_at is derived, not stored: it can never drift from the ledger, and the index
// on (product_id, created_at DESC, id DESC) makes the subquery cheap.
const productColumns = `p.id, p.sku, p.name, p.price, p.stock, p.is_active, p.created_at,
	(SELECT m.created_at FROM stock_movements m WHERE m.product_id = p.id
	 ORDER BY m.created_at DESC, m.id DESC LIMIT 1) AS last_movement_at`

func scanProduct(row pgx.CollectableRow) (Product, error) {
	var p Product
	err := row.Scan(&p.ID, &p.SKU, &p.Name, &p.Price, &p.Stock, &p.IsActive, &p.CreatedAt, &p.LastMovementAt)
	return p, err
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func (s *Store) ListProducts(ctx context.Context, f ProductFilter) ([]Product, int, error) {
	where := `($1 = '' OR p.sku ILIKE '%' || $1 || '%' OR p.name ILIKE '%' || $1 || '%')`
	switch f.Status {
	case "active":
		where += ` AND p.is_active`
	case "inactive":
		where += ` AND NOT p.is_active`
	case "low_stock":
		where += fmt.Sprintf(` AND p.stock <= %d`, LowStockThreshold)
	}
	search := likeEscaper.Replace(f.Search)

	var total int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM products p WHERE `+where, search).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(ctx,
		`SELECT `+productColumns+` FROM products p WHERE `+where+` ORDER BY p.sku LIMIT $2 OFFSET $3`,
		search, f.Limit, (f.Page-1)*f.Limit)
	if err != nil {
		return nil, 0, err
	}
	products, err := pgx.CollectRows(rows, scanProduct)
	return products, total, err
}

func (s *Store) ProductSummary(ctx context.Context) (Summary, error) {
	var sum Summary
	err := s.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT count(*),
		       count(*) FILTER (WHERE is_active),
		       COALESCE(sum(price::BIGINT * stock), 0)::BIGINT,
		       count(*) FILTER (WHERE stock <= %d)
		FROM products`, LowStockThreshold)).
		Scan(&sum.TotalProducts, &sum.ActiveProducts, &sum.TotalValue, &sum.LowStockCount)
	return sum, err
}

func getProduct(ctx context.Context, q querier, id int) (Product, error) {
	rows, err := q.Query(ctx, `SELECT `+productColumns+` FROM products p WHERE p.id = $1`, id)
	if err != nil {
		return Product{}, err
	}
	p, err := pgx.CollectExactlyOneRow(rows, scanProduct)
	if errors.Is(err, pgx.ErrNoRows) {
		return Product{}, ErrNotFound
	}
	return p, err
}

// querier is satisfied by both the pool and a transaction.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func (s *Store) CreateProduct(ctx context.Context, in ProductInput) (Product, error) {
	var id int
	err := s.db.QueryRow(ctx,
		`INSERT INTO products (sku, name, price, is_active) VALUES ($1, $2, $3, COALESCE($4, TRUE)) RETURNING id`,
		in.SKU, in.Name, in.Price, in.IsActive).Scan(&id)
	if err != nil {
		return Product{}, skuConflict(err, in.SKU)
	}
	return getProduct(ctx, s.db, id)
}

func (s *Store) UpdateProduct(ctx context.Context, id int, in ProductInput) (Product, error) {
	tag, err := s.db.Exec(ctx,
		`UPDATE products SET sku = $2, name = $3, price = $4, is_active = COALESCE($5, is_active) WHERE id = $1`,
		id, in.SKU, in.Name, in.Price, in.IsActive)
	if err != nil {
		return Product{}, skuConflict(err, in.SKU)
	}
	if tag.RowsAffected() == 0 {
		return Product{}, ErrNotFound
	}
	return getProduct(ctx, s.db, id)
}

func skuConflict(err error, sku string) error {
	switch pgCode(err) {
	case pgUniqueViolation:
		return &ConflictError{"SKU_EXISTS", fmt.Sprintf("SKU %q already exists", sku)}
	case pgForeignKeyViolation: // sales_order_items references the old sku
		return &ConflictError{"SKU_IN_USE", "SKU cannot change because orders reference it"}
	}
	return err
}

// DeleteProduct leans on the foreign keys rather than checking first: a movement inserted
// concurrently still blocks the delete, which a check-then-delete would miss.
func (s *Store) DeleteProduct(ctx context.Context, id int) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM products WHERE id = $1`, id)
	if pgCode(err) == pgForeignKeyViolation {
		return &ConflictError{"PRODUCT_HAS_HISTORY", "Product has stock history and cannot be deleted; deactivate it instead"}
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AdjustStock applies one movement. The guard lives inside the UPDATE, so concurrent
// withdrawals serialize on the row lock and the loser re-evaluates against the new stock.
func (s *Store) AdjustStock(ctx context.Context, id int, moveType string, qty int, note *string) (Product, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Product{}, err
	}
	defer tx.Rollback(ctx)

	sql := `UPDATE products SET stock = stock + $2 WHERE id = $1`
	if moveType == "out" {
		sql = `UPDATE products SET stock = stock - $2 WHERE id = $1 AND stock >= $2`
	}
	tag, err := tx.Exec(ctx, sql, id, qty)
	if pgCode(err) == pgNumericOutOfRange {
		return Product{}, &ValidationError{[]FieldError{{"qty", "qty would push stock above the maximum"}}}
	}
	if err != nil {
		return Product{}, err
	}
	if tag.RowsAffected() == 0 {
		var available int
		err := tx.QueryRow(ctx, `SELECT stock FROM products WHERE id = $1`, id).Scan(&available)
		if errors.Is(err, pgx.ErrNoRows) {
			return Product{}, ErrNotFound
		}
		if err != nil {
			return Product{}, err
		}
		return Product{}, &InsufficientStockError{Available: available}
	}

	_, err = tx.Exec(ctx, `INSERT INTO stock_movements (product_id, type, qty, note) VALUES ($1, $2, $3, $4)`, id, moveType, qty, note)
	if err != nil {
		return Product{}, err
	}
	p, err := getProduct(ctx, tx, id)
	if err != nil {
		return Product{}, err
	}
	return p, tx.Commit(ctx)
}

func (s *Store) ListMovements(ctx context.Context, productID int) ([]Movement, error) {
	if _, err := getProduct(ctx, s.db, productID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, product_id, type, qty, note, created_at FROM stock_movements
		WHERE product_id = $1 ORDER BY created_at DESC, id DESC`, productID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Movement, error) {
		var m Movement
		err := row.Scan(&m.ID, &m.ProductID, &m.Type, &m.Qty, &m.Note, &m.CreatedAt)
		return m, err
	})
}

func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}
