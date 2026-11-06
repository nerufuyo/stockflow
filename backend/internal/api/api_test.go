package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"stockflow/backend/internal/api"
	"stockflow/backend/internal/store"
)

var (
	db     *pgxpool.Pool
	server *httptest.Server
)

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://stockflow:stockflow@localhost:5433/stockflow_test?sslmode=disable"
	}
	if err := setup(dsn); err != nil {
		fmt.Fprintf(os.Stderr, "test database unavailable (run `docker compose up -d db`): %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	server.Close()
	db.Close()
	os.Exit(code)
}

func setup(dsn string) error {
	ctx := context.Background()
	target, err := url.Parse(dsn)
	if err != nil {
		return err
	}
	name := strings.TrimPrefix(target.Path, "/")
	admin := *target
	admin.Path = "/postgres"
	conn, err := pgx.Connect(ctx, admin.String())
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %q WITH (FORCE)`, name)); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %q`, name)); err != nil {
		return err
	}

	if db, err = pgxpool.New(ctx, dsn); err != nil {
		return err
	}
	schema, err := os.ReadFile("../../migrations/001_schema.sql")
	if err != nil {
		return err
	}
	if _, err := db.Exec(ctx, string(schema)); err != nil {
		return err
	}
	server = httptest.NewServer(api.Handler(store.New(db)))
	return nil
}

func reset(t *testing.T) {
	t.Helper()
	_, err := db.Exec(context.Background(),
		`TRUNCATE sales_order_items, sales_orders, stock_movements, products RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func (r response) decode(t *testing.T, dst any) {
	t.Helper()
	if err := json.Unmarshal(r.body, dst); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
}

// send uses t.Error rather than t.Fatal because tests call it from goroutines.
func send(t *testing.T, method, path string, body any, headers ...string) response {
	t.Helper()
	var reader *bytes.Reader
	if raw, ok := body.(string); ok {
		reader = bytes.NewReader([]byte(raw))
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Error(err)
			return response{}
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, server.URL+path, reader)
	if err != nil {
		t.Error(err)
		return response{}
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Error(err)
		return response{}
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(res.Body)
	return response{res.StatusCode, res.Header, buf.Bytes()}
}

func expectStatus(t *testing.T, r response, want int) {
	t.Helper()
	if r.status != want {
		t.Fatalf("status = %d, want %d; body: %s", r.status, want, r.body)
	}
}

type apiError struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		SKU       string `json:"sku"`
		Available *int   `json:"available"`
		Details   []struct {
			Field   string `json:"field"`
			Message string `json:"message"`
		} `json:"details"`
	} `json:"error"`
}

func addProduct(t *testing.T, sku string, price, stock int) store.Product {
	t.Helper()
	r := send(t, "POST", "/api/products", map[string]any{"sku": sku, "name": "Product " + sku, "price": price})
	expectStatus(t, r, http.StatusCreated)
	var p store.Product
	r.decode(t, &p)
	if stock > 0 {
		r = send(t, "POST", fmt.Sprintf("/api/products/%d/stock", p.ID), map[string]any{"type": "in", "qty": stock, "note": "opening"})
		expectStatus(t, r, http.StatusOK)
		r.decode(t, &p)
	}
	return p
}

func queryInt(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func stockOf(t *testing.T, sku string) int {
	t.Helper()
	return queryInt(t, `SELECT stock FROM products WHERE sku = $1`, sku)
}

func counts(t *testing.T) [3]int {
	t.Helper()
	return [3]int{
		queryInt(t, `SELECT count(*) FROM sales_orders`),
		queryInt(t, `SELECT count(*) FROM sales_order_items`),
		queryInt(t, `SELECT count(*) FROM stock_movements`),
	}
}

func expectLedgerConsistent(t *testing.T) {
	t.Helper()
	broken := queryInt(t, `
		SELECT count(*) FROM products p
		WHERE p.stock <> (SELECT COALESCE(sum(CASE m.type WHEN 'in' THEN m.qty ELSE -m.qty END), 0)
		                  FROM stock_movements m WHERE m.product_id = p.id)`)
	if broken != 0 {
		t.Errorf("%d products have stock that disagrees with their movements", broken)
	}
}

type line = map[string]any

func orderBody(customer string, lines ...line) map[string]any {
	return map[string]any{"customer": customer, "items": lines}
}

func TestOrderInsufficientStockWritesNothing(t *testing.T) {
	reset(t)
	addProduct(t, "A", 1000, 10)
	addProduct(t, "B", 2000, 1)
	before := counts(t)

	// A sorts first, so its decrement really happens before B fails: only rollback can undo it.
	r := send(t, "POST", "/api/orders", orderBody("Ann", line{"sku": "A", "qty": 5}, line{"sku": "B", "qty": 2}))

	expectStatus(t, r, http.StatusConflict)
	var e apiError
	r.decode(t, &e)
	if e.Error.Code != "INSUFFICIENT_STOCK" || e.Error.SKU != "B" || e.Error.Available == nil || *e.Error.Available != 1 {
		t.Errorf("error = %s", r.body)
	}
	if got := stockOf(t, "A"); got != 10 {
		t.Errorf("stock A = %d, want untouched 10", got)
	}
	if got := stockOf(t, "B"); got != 1 {
		t.Errorf("stock B = %d, want untouched 1", got)
	}
	if after := counts(t); after != before {
		t.Errorf("rows changed: %v -> %v", before, after)
	}
}

func TestOrderHappyPath(t *testing.T) {
	reset(t)
	addProduct(t, "A", 1500, 10)
	addProduct(t, "B", 20000, 3)

	r := send(t, "POST", "/api/orders", orderBody(" Ann ",
		line{"sku": "B", "qty": 3, "unit_price": 1},
		line{"sku": "A", "qty": 2, "price": 1}))

	expectStatus(t, r, http.StatusCreated)
	var o store.Order
	r.decode(t, &o)
	if o.Customer != "Ann" || o.Status != "CREATED" || o.Total != 2*1500+3*20000 || len(o.Items) != 2 {
		t.Errorf("order = %+v", o)
	}
	for _, it := range o.Items {
		want := map[string]int{"A": 1500, "B": 20000}[it.SKU]
		if it.UnitPrice != want {
			t.Errorf("unit_price %s = %d, want %d", it.SKU, it.UnitPrice, want)
		}
	}
	if got := stockOf(t, "A"); got != 8 {
		t.Errorf("stock A = %d, want 8", got)
	}
	if got := stockOf(t, "B"); got != 0 {
		t.Errorf("stock B = %d, want 0", got)
	}
	note := fmt.Sprintf("Order #%d", o.ID)
	if got := queryInt(t, `SELECT count(*) FROM stock_movements WHERE type = 'out' AND note = $1`, note); got != 2 {
		t.Errorf("out movements = %d, want 2", got)
	}
	expectLedgerConsistent(t)

	var fetched store.Order
	r = send(t, "GET", fmt.Sprintf("/api/orders/%d", o.ID), nil)
	expectStatus(t, r, http.StatusOK)
	r.decode(t, &fetched)
	if fetched.Total != o.Total || len(fetched.Items) != 2 {
		t.Errorf("GET order = %+v", fetched)
	}
}

func TestOrderTotalBeyondInt32(t *testing.T) {
	reset(t)
	addProduct(t, "EXPENSIVE", 2_000_000_000, 5)
	r := send(t, "POST", "/api/orders", orderBody("Ann", line{"sku": "EXPENSIVE", "qty": 5}))
	expectStatus(t, r, http.StatusCreated)
	var o store.Order
	r.decode(t, &o)
	if o.Total != 10_000_000_000 {
		t.Errorf("total = %d", o.Total)
	}
}

func TestOrderValidationWritesNothing(t *testing.T) {
	reset(t)
	addProduct(t, "A", 1000, 10)
	inactive := addProduct(t, "OLD", 1000, 10)
	expectStatus(t, send(t, "PUT", fmt.Sprintf("/api/products/%d", inactive.ID),
		map[string]any{"sku": "OLD", "name": "Old", "price": 1000, "is_active": false}), http.StatusOK)
	before := counts(t)

	cases := []struct {
		name  string
		body  any
		field string
	}{
		{"empty items", orderBody("Ann"), "items"},
		{"missing items", `{"customer":"Ann"}`, "items"},
		{"missing customer", orderBody(" ", line{"sku": "A", "qty": 1}), "customer"},
		{"unknown sku", orderBody("Ann", line{"sku": "A", "qty": 1}, line{"sku": "NOPE", "qty": 1}), "items[1].sku"},
		{"inactive sku", orderBody("Ann", line{"sku": "OLD", "qty": 1}), "items[0].sku"},
		{"zero qty", orderBody("Ann", line{"sku": "A", "qty": 0}), "items[0].qty"},
		{"negative qty", orderBody("Ann", line{"sku": "A", "qty": -1}), "items[0].qty"},
		{"huge qty", orderBody("Ann", line{"sku": "A", "qty": 3_000_000_000}), "items[0].qty"},
		{"duplicate sku", orderBody("Ann", line{"sku": "A", "qty": 1}, line{"sku": " A ", "qty": 1}), "items[1].sku"},
		{"blank sku", orderBody("Ann", line{"sku": "", "qty": 1}), "items[0].sku"},
		{"fractional qty", `{"customer":"Ann","items":[{"sku":"A","qty":1.5}]}`, "items.qty"},
		{"malformed json", `{"customer":`, "body"},
		{"not an object", `[]`, "body"},
		{"item is not an object", `{"customer":"Ann","items":["A"]}`, "items"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := send(t, "POST", "/api/orders", tc.body)
			expectStatus(t, r, http.StatusBadRequest)
			var e apiError
			r.decode(t, &e)
			if e.Error.Code != "VALIDATION_ERROR" || e.Error.Message == "" {
				t.Errorf("error = %s", r.body)
			}
			if !strings.Contains(string(r.body), `"field":"`+tc.field+`"`) {
				t.Errorf("no detail for field %q: %s", tc.field, r.body)
			}
		})
	}
	if after := counts(t); after != before {
		t.Errorf("rows changed: %v -> %v", before, after)
	}
	if got := stockOf(t, "A"); got != 10 {
		t.Errorf("stock A = %d", got)
	}
}

func TestStockAdjustment(t *testing.T) {
	reset(t)
	p := addProduct(t, "A", 1000, 5)
	path := fmt.Sprintf("/api/products/%d/stock", p.ID)

	t.Run("out writes movement and updates stock", func(t *testing.T) {
		r := send(t, "POST", path, map[string]any{"type": "out", "qty": 2, "note": "damaged"})
		expectStatus(t, r, http.StatusOK)
		var got store.Product
		r.decode(t, &got)
		if got.Stock != 3 || got.LastMovementAt == nil {
			t.Errorf("product = %+v", got)
		}
		var ms struct{ Data []store.Movement }
		send(t, "GET", fmt.Sprintf("/api/products/%d/movements", p.ID), nil).decode(t, &ms)
		if len(ms.Data) != 2 || ms.Data[0].Type != "out" || ms.Data[0].Qty != 2 || *ms.Data[0].Note != "damaged" {
			t.Errorf("movements (newest first) = %+v", ms.Data)
		}
	})

	t.Run("overdraw rejected with nothing written", func(t *testing.T) {
		before := counts(t)
		r := send(t, "POST", path, map[string]any{"type": "out", "qty": 4})
		expectStatus(t, r, http.StatusConflict)
		var e apiError
		r.decode(t, &e)
		if e.Error.Code != "INSUFFICIENT_STOCK" || e.Error.Available == nil || *e.Error.Available != 3 {
			t.Errorf("error = %s", r.body)
		}
		if got := stockOf(t, "A"); got != 3 {
			t.Errorf("stock = %d", got)
		}
		if after := counts(t); after != before {
			t.Errorf("rows changed: %v -> %v", before, after)
		}
	})

	t.Run("available 0 is still serialized", func(t *testing.T) {
		empty := addProduct(t, "EMPTY", 1000, 0)
		r := send(t, "POST", fmt.Sprintf("/api/products/%d/stock", empty.ID), map[string]any{"type": "out", "qty": 1})
		expectStatus(t, r, http.StatusConflict)
		if !strings.Contains(string(r.body), `"available":0`) {
			t.Errorf("body = %s", r.body)
		}
	})

	t.Run("overflow is a client error and writes nothing", func(t *testing.T) {
		before := counts(t)
		r := send(t, "POST", path, map[string]any{"type": "in", "qty": 2147483647})
		expectStatus(t, r, http.StatusBadRequest)
		if after := counts(t); after != before {
			t.Errorf("rows changed: %v -> %v", before, after)
		}
	})

	t.Run("validation", func(t *testing.T) {
		for _, body := range []any{
			map[string]any{"type": "sideways", "qty": 1},
			map[string]any{"type": "in", "qty": 0},
			map[string]any{"type": "in", "qty": -3},
			`{"type":"in","qty":1.5}`,
			`{"type":"in","qty":"2"}`,
		} {
			expectStatus(t, send(t, "POST", path, body), http.StatusBadRequest)
		}
	})

	t.Run("missing product", func(t *testing.T) {
		expectStatus(t, send(t, "POST", "/api/products/9999/stock", map[string]any{"type": "in", "qty": 1}), http.StatusNotFound)
		expectStatus(t, send(t, "POST", "/api/products/9999/stock", map[string]any{"type": "out", "qty": 1}), http.StatusNotFound)
		expectStatus(t, send(t, "GET", "/api/products/9999/movements", nil), http.StatusNotFound)
	})
	expectLedgerConsistent(t)
}

func TestConcurrentOrdersNeverOversell(t *testing.T) {
	reset(t)
	addProduct(t, "A", 1000, 10)

	const attempts = 40
	var wg sync.WaitGroup
	statuses := make([]int, attempts)
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i] = send(t, "POST", "/api/orders", orderBody("Ann", line{"sku": "A", "qty": 1})).status
		}()
	}
	wg.Wait()

	created, conflicts := 0, 0
	for _, status := range statuses {
		switch status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
		default:
			t.Errorf("unexpected status %d", status)
		}
	}
	if created != 10 || conflicts != attempts-10 {
		t.Errorf("created = %d, conflicts = %d", created, conflicts)
	}
	if got := stockOf(t, "A"); got != 0 {
		t.Errorf("stock = %d, want 0", got)
	}
	expectLedgerConsistent(t)
}

func TestConcurrentMultiItemOrdersDoNotDeadlock(t *testing.T) {
	reset(t)
	addProduct(t, "A", 1000, 30)
	addProduct(t, "B", 1000, 30)

	const attempts = 40
	var wg sync.WaitGroup
	statuses := make([]int, attempts)
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lines := []line{{"sku": "A", "qty": 1}, {"sku": "B", "qty": 1}}
			if i%2 == 0 {
				lines[0], lines[1] = lines[1], lines[0]
			}
			statuses[i] = send(t, "POST", "/api/orders", orderBody("Ann", lines...)).status
		}()
	}
	wg.Wait()

	created := 0
	for _, status := range statuses {
		switch status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
		default:
			t.Errorf("unexpected status %d (a deadlock surfaces as 500)", status)
		}
	}
	if created != 30 {
		t.Errorf("created = %d, want 30", created)
	}
	if a, b := stockOf(t, "A"), stockOf(t, "B"); a != 0 || b != 0 {
		t.Errorf("stock = %d, %d; want 0, 0", a, b)
	}
	expectLedgerConsistent(t)
}

func TestConcurrentStockOutNeverOverdraws(t *testing.T) {
	reset(t)
	p := addProduct(t, "A", 1000, 7)

	const attempts = 30
	var wg sync.WaitGroup
	statuses := make([]int, attempts)
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i] = send(t, "POST", fmt.Sprintf("/api/products/%d/stock", p.ID), map[string]any{"type": "out", "qty": 1}).status
		}()
	}
	wg.Wait()

	ok := 0
	for _, status := range statuses {
		if status == http.StatusOK {
			ok++
		} else if status != http.StatusConflict {
			t.Errorf("unexpected status %d", status)
		}
	}
	if ok != 7 || stockOf(t, "A") != 0 {
		t.Errorf("ok = %d, stock = %d", ok, stockOf(t, "A"))
	}
	expectLedgerConsistent(t)
}

func TestCancelOrder(t *testing.T) {
	reset(t)
	addProduct(t, "A", 1000, 10)
	r := send(t, "POST", "/api/orders", orderBody("Ann", line{"sku": "A", "qty": 4}))
	expectStatus(t, r, http.StatusCreated)
	var o store.Order
	r.decode(t, &o)
	path := fmt.Sprintf("/api/orders/%d/cancel", o.ID)

	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i := range statuses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i] = send(t, "POST", path, nil).status
		}()
	}
	wg.Wait()
	if statuses[0]+statuses[1] != http.StatusOK+http.StatusConflict {
		t.Errorf("statuses = %v, want one 200 and one 409", statuses)
	}
	if got := stockOf(t, "A"); got != 10 {
		t.Errorf("stock = %d, want restored 10", got)
	}
	expectLedgerConsistent(t)

	r = send(t, "POST", path, nil)
	expectStatus(t, r, http.StatusConflict)
	var e apiError
	r.decode(t, &e)
	if e.Error.Code != "ORDER_ALREADY_CANCELLED" {
		t.Errorf("error = %s", r.body)
	}

	var fetched store.Order
	send(t, "GET", fmt.Sprintf("/api/orders/%d", o.ID), nil).decode(t, &fetched)
	if fetched.Status != "CANCELLED" || fetched.CancelledAt == nil {
		t.Errorf("order = %+v", fetched)
	}
	expectStatus(t, send(t, "POST", "/api/orders/9999/cancel", nil), http.StatusNotFound)
	expectStatus(t, send(t, "GET", "/api/orders/9999", nil), http.StatusNotFound)
	expectStatus(t, send(t, "GET", "/api/orders/abc", nil), http.StatusNotFound)
	expectStatus(t, send(t, "GET", "/api/orders/99999999999999", nil), http.StatusNotFound)
}

func TestProductCRUD(t *testing.T) {
	reset(t)

	t.Run("create validation", func(t *testing.T) {
		for _, body := range []any{
			map[string]any{"name": "x", "price": 1},
			map[string]any{"sku": "  ", "name": "x", "price": 1},
			map[string]any{"sku": "X", "price": 1},
			map[string]any{"sku": "X", "name": "x"},
			map[string]any{"sku": "X", "name": "x", "price": -1},
			map[string]any{"sku": "X", "name": "x", "price": 3_000_000_000},
			map[string]any{"sku": "X\x00", "name": "x", "price": 1},
			`{"sku":"X","name":"x","price":12.5}`,
			`{"sku":"X","name":"x","price":"12"}`,
		} {
			r := send(t, "POST", "/api/products", body)
			expectStatus(t, r, http.StatusBadRequest)
		}
	})

	p := addProduct(t, "A", 0, 0)
	if !p.IsActive || p.Price != 0 || p.Stock != 0 || p.LastMovementAt != nil {
		t.Errorf("created = %+v", p)
	}

	t.Run("duplicate sku", func(t *testing.T) {
		r := send(t, "POST", "/api/products", map[string]any{"sku": "A", "name": "x", "price": 1})
		expectStatus(t, r, http.StatusConflict)
		var e apiError
		r.decode(t, &e)
		if e.Error.Code != "SKU_EXISTS" {
			t.Errorf("error = %s", r.body)
		}
	})

	t.Run("update", func(t *testing.T) {
		path := fmt.Sprintf("/api/products/%d", p.ID)
		r := send(t, "PUT", path, map[string]any{"sku": "A2", "name": "Renamed", "price": 5})
		expectStatus(t, r, http.StatusOK)
		var got store.Product
		r.decode(t, &got)
		if got.SKU != "A2" || got.Name != "Renamed" || got.Price != 5 || !got.IsActive {
			t.Errorf("updated = %+v", got)
		}
		expectStatus(t, send(t, "PUT", "/api/products/9999", map[string]any{"sku": "Z", "name": "z", "price": 1}), http.StatusNotFound)
		expectStatus(t, send(t, "PUT", path, map[string]any{"sku": "", "name": "z", "price": 1}), http.StatusBadRequest)
	})

	t.Run("update cannot rename a sku used by orders", func(t *testing.T) {
		sold := addProduct(t, "SOLD", 100, 5)
		expectStatus(t, send(t, "POST", "/api/orders", orderBody("Ann", line{"sku": "SOLD", "qty": 1})), http.StatusCreated)
		r := send(t, "PUT", fmt.Sprintf("/api/products/%d", sold.ID), map[string]any{"sku": "NEW", "name": "n", "price": 1})
		expectStatus(t, r, http.StatusConflict)
		var e apiError
		r.decode(t, &e)
		if e.Error.Code != "SKU_IN_USE" {
			t.Errorf("error = %s", r.body)
		}
	})

	t.Run("delete", func(t *testing.T) {
		expectStatus(t, send(t, "DELETE", "/api/products/9999", nil), http.StatusNotFound)
		expectStatus(t, send(t, "DELETE", fmt.Sprintf("/api/products/%d", p.ID), nil), http.StatusNoContent)
		expectStatus(t, send(t, "DELETE", fmt.Sprintf("/api/products/%d", p.ID), nil), http.StatusNotFound)

		withHistory := addProduct(t, "HIST", 100, 1)
		r := send(t, "DELETE", fmt.Sprintf("/api/products/%d", withHistory.ID), nil)
		expectStatus(t, r, http.StatusConflict)
		var e apiError
		r.decode(t, &e)
		if e.Error.Code != "PRODUCT_HAS_HISTORY" {
			t.Errorf("error = %s", r.body)
		}
		if got := queryInt(t, `SELECT count(*) FROM stock_movements WHERE product_id = $1`, withHistory.ID); got != 1 {
			t.Errorf("history rows = %d", got)
		}
	})
}

func TestListProducts(t *testing.T) {
	reset(t)
	addProduct(t, "CBL-1", 100, 50)
	addProduct(t, "cbl-2", 100, 10)
	addProduct(t, "MSE-1", 100, 0)
	addProduct(t, "100%_OFF", 100, 20)
	off := addProduct(t, "OLD-1", 100, 11)
	expectStatus(t, send(t, "PUT", fmt.Sprintf("/api/products/%d", off.ID),
		map[string]any{"sku": "OLD-1", "name": "Cable Old", "price": 100, "is_active": false}), http.StatusOK)

	type page struct {
		Data  []store.Product `json:"data"`
		Page  int             `json:"page"`
		Limit int             `json:"limit"`
		Total int             `json:"total"`
	}
	list := func(t *testing.T, query string) page {
		t.Helper()
		r := send(t, "GET", "/api/products?"+query, nil)
		expectStatus(t, r, http.StatusOK)
		var p page
		r.decode(t, &p)
		return p
	}

	for _, tc := range []struct {
		query string
		total int
	}{
		{"", 5},
		{"search=cbl", 2},
		{"search=CABLE", 1},
		{"search=%25", 1},
		{"search=_", 1},
		{"search=nothing-here", 0},
		{"status=active", 4},
		{"status=inactive", 1},
		{"status=low_stock", 2},
		{"search=cbl&status=low_stock", 1},
	} {
		if got := list(t, tc.query); got.Total != tc.total || len(got.Data) != tc.total {
			t.Errorf("?%s: total = %d, rows = %d, want %d", tc.query, got.Total, len(got.Data), tc.total)
		}
	}

	t.Run("empty result is an array", func(t *testing.T) {
		r := send(t, "GET", "/api/products?search=nothing-here", nil)
		if !strings.Contains(string(r.body), `"data":[]`) {
			t.Errorf("body = %s", r.body)
		}
	})

	t.Run("pagination", func(t *testing.T) {
		got := list(t, "page=2&limit=2")
		if got.Page != 2 || got.Limit != 2 || got.Total != 5 || len(got.Data) != 2 {
			t.Errorf("page = %+v", got)
		}
		if got := list(t, "limit=100000"); got.Limit != 100 {
			t.Errorf("limit not capped: %d", got.Limit)
		}
		for _, q := range []string{"page=0", "page=abc", "limit=-1", "status=bogus", "search=%ff", "search=%00"} {
			expectStatus(t, send(t, "GET", "/api/products?"+q, nil), http.StatusBadRequest)
		}
	})

	t.Run("last_movement_at", func(t *testing.T) {
		none := addProduct(t, "NONE", 1, 0)
		r := send(t, "GET", "/api/products?search=NONE", nil)
		if !strings.Contains(string(r.body), `"last_movement_at":null`) {
			t.Errorf("no null last_movement_at for %d: %s", none.ID, r.body)
		}
		if got := list(t, "search=MSE"); got.Data[0].LastMovementAt != nil {
			t.Errorf("unexpected movement: %+v", got.Data[0])
		}
		if got := list(t, "search=CBL-1"); got.Data[0].LastMovementAt == nil {
			t.Error("expected last_movement_at")
		}
	})

	t.Run("movements of a product without history are an empty array", func(t *testing.T) {
		none := list(t, "search=NONE").Data[0]
		r := send(t, "GET", fmt.Sprintf("/api/products/%d/movements", none.ID), nil)
		if !strings.Contains(string(r.body), `"data":[]`) {
			t.Errorf("body = %s", r.body)
		}
	})
}

func TestSummary(t *testing.T) {
	reset(t)
	addProduct(t, "A", 1000, 10)
	off := addProduct(t, "B", 2_000_000_000, 11)
	expectStatus(t, send(t, "PUT", fmt.Sprintf("/api/products/%d", off.ID),
		map[string]any{"sku": "B", "name": "B", "price": 2_000_000_000, "is_active": false}), http.StatusOK)

	var s store.Summary
	send(t, "GET", "/api/products/summary", nil).decode(t, &s)
	want := store.Summary{TotalProducts: 2, ActiveProducts: 1, TotalValue: 10*1000 + 11*2_000_000_000, LowStockCount: 1}
	if s != want {
		t.Errorf("summary = %+v, want %+v", s, want)
	}
}

func TestIdempotencyKey(t *testing.T) {
	reset(t)
	addProduct(t, "A", 1000, 100)
	body := orderBody("Ann", line{"sku": "A", "qty": 1})

	const attempts = 10
	var wg sync.WaitGroup
	results := make([]response, attempts)
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = send(t, "POST", "/api/orders", body, "Idempotency-Key", "retry-1")
		}()
	}
	wg.Wait()

	replays := 0
	ids := map[int]bool{}
	for _, r := range results {
		expectStatus(t, r, http.StatusCreated)
		var o store.Order
		r.decode(t, &o)
		ids[o.ID] = true
		if o.Total != 1000 || len(o.Items) != 1 {
			t.Errorf("order = %+v", o)
		}
		if r.header.Get("Idempotency-Replayed") == "true" {
			replays++
		}
	}
	if len(ids) != 1 || replays != attempts-1 {
		t.Errorf("order ids = %v, replays = %d", ids, replays)
	}
	if got := stockOf(t, "A"); got != 99 {
		t.Errorf("stock = %d, want 99", got)
	}

	t.Run("same key, different request", func(t *testing.T) {
		r := send(t, "POST", "/api/orders", orderBody("Ann", line{"sku": "A", "qty": 2}), "Idempotency-Key", "retry-1")
		expectStatus(t, r, http.StatusConflict)
		var e apiError
		r.decode(t, &e)
		if e.Error.Code != "IDEMPOTENCY_KEY_REUSED" {
			t.Errorf("error = %s", r.body)
		}
	})

	t.Run("a failed attempt does not burn the key", func(t *testing.T) {
		tooMany := orderBody("Ann", line{"sku": "A", "qty": 1000})
		expectStatus(t, send(t, "POST", "/api/orders", tooMany, "Idempotency-Key", "retry-2"), http.StatusConflict)
		expectStatus(t, send(t, "POST", "/api/products/1/stock", map[string]any{"type": "in", "qty": 1000}), http.StatusOK)
		expectStatus(t, send(t, "POST", "/api/orders", tooMany, "Idempotency-Key", "retry-2"), http.StatusCreated)
	})
}

func TestDailySales(t *testing.T) {
	reset(t)
	addProduct(t, "A", 1000, 10)
	expectStatus(t, send(t, "POST", "/api/orders", orderBody("Ann", line{"sku": "A", "qty": 2})), http.StatusCreated)
	r := send(t, "POST", "/api/orders", orderBody("Bob", line{"sku": "A", "qty": 1}))
	var cancelled store.Order
	r.decode(t, &cancelled)
	expectStatus(t, send(t, "POST", fmt.Sprintf("/api/orders/%d/cancel", cancelled.ID), nil), http.StatusOK)

	var today store.DailySales
	send(t, "GET", "/api/reports/daily-sales", nil).decode(t, &today)
	if today.Orders != 1 || today.Total != 2000 || today.Date == "" {
		t.Errorf("today = %+v", today)
	}
	var old store.DailySales
	send(t, "GET", "/api/reports/daily-sales?date=2001-01-01", nil).decode(t, &old)
	if old.Date != "2001-01-01" || old.Orders != 0 || old.Total != 0 {
		t.Errorf("old = %+v", old)
	}
	expectStatus(t, send(t, "GET", "/api/reports/daily-sales?date=yesterday", nil), http.StatusBadRequest)
}

func TestSeedDataIsConsistent(t *testing.T) {
	reset(t)
	seed, err := os.ReadFile("../../migrations/002_seed.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(context.Background(), string(seed)); err != nil {
		t.Fatal(err)
	}
	defer reset(t)

	if got := queryInt(t, `SELECT count(*) FROM products`); got < 12 {
		t.Errorf("products = %d, want 12+", got)
	}
	if got := queryInt(t, `SELECT count(*) FROM sales_orders`); got < 2 {
		t.Errorf("orders = %d, want 2+", got)
	}
	expectLedgerConsistent(t)
	if got := queryInt(t, `
		SELECT count(*) FROM sales_orders o
		WHERE o.total <> (SELECT sum(i.qty::BIGINT * i.unit_price) FROM sales_order_items i WHERE i.order_id = o.id)`); got != 0 {
		t.Errorf("%d orders have a wrong total", got)
	}
}

func TestUnknownRouteUsesErrorEnvelope(t *testing.T) {
	r := send(t, "GET", "/api/nope", nil)
	expectStatus(t, r, http.StatusNotFound)
	var e apiError
	r.decode(t, &e)
	if e.Error.Code != "NOT_FOUND" {
		t.Errorf("error = %s", r.body)
	}
}
