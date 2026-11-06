// Package api is the HTTP layer: request parsing, validation of request shape, and the
// translation of store errors into the JSON error envelope.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"stockflow/backend/internal/store"
)

const (
	defaultLimit = 20
	maxLimit     = 100
	maxBodyBytes = 1 << 20
)

type API struct{ store *store.Store }

// Handler returns the routes for the whole service.
func Handler(s *store.Store) http.Handler {
	a := &API{store: s}
	mux := http.NewServeMux()
	route := func(pattern string, h handlerFunc) { mux.HandleFunc(pattern, wrap(h)) }

	route("GET /healthz", a.health)
	route("GET /api/products", a.listProducts)
	route("GET /api/products/summary", a.productSummary)
	route("POST /api/products", a.createProduct)
	route("PUT /api/products/{id}", a.updateProduct)
	route("DELETE /api/products/{id}", a.deleteProduct)
	route("POST /api/products/{id}/stock", a.adjustStock)
	route("GET /api/products/{id}/movements", a.listMovements)
	route("GET /api/orders", a.listOrders)
	route("POST /api/orders", a.createOrder)
	route("GET /api/orders/{id}", a.getOrder)
	route("POST /api/orders/{id}/cancel", a.cancelOrder)
	route("GET /api/reports/daily-sales", a.dailySales)
	route("/", func(http.ResponseWriter, *http.Request) error { return store.ErrNotFound })
	return mux
}

type handlerFunc func(http.ResponseWriter, *http.Request) error

func wrap(h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			writeError(w, r, err)
		}
	}
}

// errorBody is the one error shape every endpoint returns. Available is a pointer because
// 0 is a meaningful value that must still be serialized.
type errorBody struct {
	Code      string             `json:"code"`
	Message   string             `json:"message"`
	SKU       string             `json:"sku,omitempty"`
	Available *int               `json:"available,omitempty"`
	Details   []store.FieldError `json:"details,omitempty"`
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		validation *store.ValidationError
		stock      *store.InsufficientStockError
		conflict   *store.ConflictError
	)
	status, body := http.StatusInternalServerError, errorBody{Code: "INTERNAL_ERROR", Message: "Something went wrong"}
	switch {
	case errors.As(err, &validation):
		status = http.StatusBadRequest
		body = errorBody{Code: "VALIDATION_ERROR", Message: validationSummary(validation.Details), Details: validation.Details}
	case errors.As(err, &stock):
		status = http.StatusConflict
		body = errorBody{Code: "INSUFFICIENT_STOCK", SKU: stock.SKU, Available: &stock.Available,
			Message: fmt.Sprintf("Insufficient stock: %d available", stock.Available)}
	case errors.As(err, &conflict):
		status = http.StatusConflict
		body = errorBody{Code: conflict.Code, Message: conflict.Message}
	case errors.Is(err, store.ErrNotFound):
		status = http.StatusNotFound
		body = errorBody{Code: "NOT_FOUND", Message: "Resource not found"}
	default:
		slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	}
	writeJSON(w, status, map[string]errorBody{"error": body})
}

func validationSummary(details []store.FieldError) string {
	messages := make([]string, len(details))
	for i, d := range details {
		messages[i] = d.Message
	}
	return strings.Join(messages, "; ")
}

func writeJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

func invalid(field, message string) error {
	return &store.ValidationError{Details: []store.FieldError{{Field: field, Message: message}}}
}

// decodeJSON turns malformed bodies and wrong types (e.g. a fractional price) into 400s.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(dst)
	if err == nil {
		return nil
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		if typeErr.Field == "" {
			return invalid("body", "request body must be a JSON object")
		}
		expected := map[reflect.Kind]string{reflect.Int: "an integer", reflect.String: "a string", reflect.Bool: "a boolean", reflect.Slice: "an array"}
		return invalid(typeErr.Field, fmt.Sprintf("%s must be %s", typeErr.Field, expected[typeErr.Type.Kind()]))
	}
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return invalid("body", "request body is too large")
	}
	return invalid("body", "request body must be valid JSON")
}

// pathID treats anything that cannot be a row id as a row that does not exist.
func pathID(r *http.Request) (int, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 32)
	if err != nil || id <= 0 {
		return 0, store.ErrNotFound
	}
	return int(id), nil
}

func paging(r *http.Request) (page, limit int, err error) {
	parse := func(name string, fallback int) (int, error) {
		raw := r.URL.Query().Get(name)
		if raw == "" {
			return fallback, nil
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > math.MaxInt32 {
			return 0, invalid(name, name+" must be a positive integer")
		}
		return n, nil
	}
	if page, err = parse("page", 1); err != nil {
		return 0, 0, err
	}
	if limit, err = parse("limit", defaultLimit); err != nil {
		return 0, 0, err
	}
	return page, min(limit, maxLimit), nil
}

type pageResponse[T any] struct {
	Data  []T `json:"data"`
	Page  int `json:"page"`
	Limit int `json:"limit"`
	Total int `json:"total"`
}

func (a *API) health(w http.ResponseWriter, r *http.Request) error {
	if err := a.store.Ping(r.Context()); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) listProducts(w http.ResponseWriter, r *http.Request) error {
	page, limit, err := paging(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	status := q.Get("status")
	switch status {
	case "", "active", "inactive", "low_stock":
	default:
		return invalid("status", "status must be one of active, inactive, low_stock")
	}
	products, total, err := a.store.ListProducts(r.Context(), store.ProductFilter{
		Search: strings.ReplaceAll(strings.TrimSpace(q.Get("search")), "\x00", ""),
		Status: status, Page: page, Limit: limit,
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, pageResponse[store.Product]{products, page, limit, total})
}

func (a *API) productSummary(w http.ResponseWriter, r *http.Request) error {
	summary, err := a.store.ProductSummary(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, summary)
}

type productRequest struct {
	SKU      string `json:"sku"`
	Name     string `json:"name"`
	Price    *int   `json:"price"` // pointer: a missing price differs from a price of 0
	IsActive *bool  `json:"is_active"`
}

func (req productRequest) validate() (store.ProductInput, error) {
	in := store.ProductInput{SKU: strings.TrimSpace(req.SKU), Name: strings.TrimSpace(req.Name), IsActive: req.IsActive}
	var details []store.FieldError
	add := func(field, message string) {
		details = append(details, store.FieldError{Field: field, Message: message})
	}

	if in.SKU == "" {
		add("sku", "sku is required")
	} else if hasNUL(in.SKU) {
		add("sku", "sku contains invalid characters")
	}
	if in.Name == "" {
		add("name", "name is required")
	} else if hasNUL(in.Name) {
		add("name", "name contains invalid characters")
	}
	switch {
	case req.Price == nil:
		add("price", "price is required")
	case *req.Price < 0:
		add("price", "price must be 0 or greater")
	case *req.Price > math.MaxInt32:
		add("price", "price is too large")
	default:
		in.Price = *req.Price
	}
	if details != nil {
		return in, &store.ValidationError{Details: details}
	}
	return in, nil
}

// hasNUL guards text columns: Postgres rejects NUL bytes, which would otherwise surface as a 500.
func hasNUL(s string) bool { return strings.ContainsRune(s, 0) }

func (a *API) createProduct(w http.ResponseWriter, r *http.Request) error {
	var req productRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	in, err := req.validate()
	if err != nil {
		return err
	}
	product, err := a.store.CreateProduct(r.Context(), in)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, product)
}

func (a *API) updateProduct(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var req productRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	in, err := req.validate()
	if err != nil {
		return err
	}
	product, err := a.store.UpdateProduct(r.Context(), id, in)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, product)
}

func (a *API) deleteProduct(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if err := a.store.DeleteProduct(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *API) adjustStock(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var req struct {
		Type string `json:"type"`
		Qty  int    `json:"qty"`
		Note string `json:"note"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	var details []store.FieldError
	if req.Type != "in" && req.Type != "out" {
		details = append(details, store.FieldError{Field: "type", Message: `type must be "in" or "out"`})
	}
	if req.Qty <= 0 || req.Qty > math.MaxInt32 {
		details = append(details, store.FieldError{Field: "qty", Message: "qty must be a positive integer"})
	}
	if hasNUL(req.Note) {
		details = append(details, store.FieldError{Field: "note", Message: "note contains invalid characters"})
	}
	if details != nil {
		return &store.ValidationError{Details: details}
	}
	var note *string
	if trimmed := strings.TrimSpace(req.Note); trimmed != "" {
		note = &trimmed
	}
	product, err := a.store.AdjustStock(r.Context(), id, req.Type, req.Qty, note)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, product)
}

func (a *API) listMovements(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	movements, err := a.store.ListMovements(r.Context(), id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string][]store.Movement{"data": movements})
}

func (a *API) createOrder(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Customer string `json:"customer"`
		Items    []struct {
			SKU string `json:"sku"`
			Qty int    `json:"qty"`
		} `json:"items"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}

	order := store.NewOrder{Customer: strings.TrimSpace(req.Customer), IdempotencyKey: r.Header.Get("Idempotency-Key")}
	var details []store.FieldError
	add := func(field, message string) {
		details = append(details, store.FieldError{Field: field, Message: message})
	}

	if order.Customer == "" {
		add("customer", "customer is required")
	} else if hasNUL(order.Customer) {
		add("customer", "customer contains invalid characters")
	}
	if len(order.IdempotencyKey) > 255 {
		add("Idempotency-Key", "Idempotency-Key must be at most 255 characters")
	}
	if len(req.Items) == 0 {
		add("items", "items must not be empty")
	}
	seen := map[string]bool{}
	for i, item := range req.Items {
		sku := strings.TrimSpace(item.SKU)
		switch {
		case sku == "":
			add(fmt.Sprintf("items[%d].sku", i), fmt.Sprintf("item %d: sku is required", i+1))
		case hasNUL(sku):
			add(fmt.Sprintf("items[%d].sku", i), fmt.Sprintf("item %d: sku contains invalid characters", i+1))
		case seen[sku]:
			add(fmt.Sprintf("items[%d].sku", i), fmt.Sprintf("duplicate SKU %q", sku))
		}
		seen[sku] = true
		if item.Qty <= 0 || item.Qty > math.MaxInt32 {
			add(fmt.Sprintf("items[%d].qty", i), fmt.Sprintf("item %d: qty must be a positive integer", i+1))
		}
		order.Lines = append(order.Lines, store.OrderLine{SKU: sku, Qty: item.Qty})
	}
	if details != nil {
		return &store.ValidationError{Details: details}
	}

	created, replayed, err := a.store.CreateOrder(r.Context(), order)
	if err != nil {
		return err
	}
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	return writeJSON(w, http.StatusCreated, created)
}

func (a *API) getOrder(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	order, err := a.store.GetOrder(r.Context(), id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, order)
}

func (a *API) cancelOrder(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	order, err := a.store.CancelOrder(r.Context(), id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, order)
}

func (a *API) listOrders(w http.ResponseWriter, r *http.Request) error {
	page, limit, err := paging(r)
	if err != nil {
		return err
	}
	orders, total, err := a.store.ListOrders(r.Context(), page, limit)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, pageResponse[store.OrderSummary]{orders, page, limit, total})
}

func (a *API) dailySales(w http.ResponseWriter, r *http.Request) error {
	var date *string
	if raw := r.URL.Query().Get("date"); raw != "" {
		if _, err := time.Parse(time.DateOnly, raw); err != nil {
			return invalid("date", "date must be formatted as YYYY-MM-DD")
		}
		date = &raw
	}
	report, err := a.store.DailySales(r.Context(), date)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, report)
}
