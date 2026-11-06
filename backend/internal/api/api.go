// Package api is the HTTP layer of StockFlow.
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
	"unicode/utf8"

	"stockflow/backend/internal/store"
)

const (
	defaultLimit = 20
	maxLimit     = 100
	maxBodyBytes = 1 << 20
)

type server struct{ store *store.Store }

func Handler(s *store.Store) http.Handler {
	srv := &server{store: s}
	mux := http.NewServeMux()
	route := func(pattern string, h handlerFunc) { mux.HandleFunc(pattern, wrap(h)) }

	route("GET /api/products", srv.listProducts)
	route("GET /api/products/summary", srv.productSummary)
	route("POST /api/products", srv.createProduct)
	route("PUT /api/products/{id}", srv.updateProduct)
	route("DELETE /api/products/{id}", srv.deleteProduct)
	route("POST /api/products/{id}/stock", srv.adjustStock)
	route("GET /api/products/{id}/movements", srv.listMovements)
	route("GET /api/orders", srv.listOrders)
	route("POST /api/orders", srv.createOrder)
	route("GET /api/orders/{id}", srv.getOrder)
	route("POST /api/orders/{id}/cancel", srv.cancelOrder)
	route("GET /api/reports/daily-sales", srv.dailySales)
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

type errorBody struct {
	Code      string             `json:"code"`
	Message   string             `json:"message"`
	SKU       string             `json:"sku,omitempty"`
	Available *int               `json:"available,omitempty"` // pointer: 0 must still be serialized
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

type fieldErrors []store.FieldError

func (e *fieldErrors) add(field, message string) {
	*e = append(*e, store.FieldError{Field: field, Message: message})
}

func (e fieldErrors) err() error {
	if len(e) == 0 {
		return nil
	}
	return &store.ValidationError{Details: e}
}

func invalid(field, message string) error {
	var errs fieldErrors
	errs.add(field, message)
	return errs.err()
}

// storable reports whether Postgres can hold s in a text column.
func storable(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

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
		return invalid(typeErr.Field, fmt.Sprintf("%s must be %s", typeErr.Field, jsonTypeName(typeErr.Type.Kind())))
	}
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return invalid("body", "request body is too large")
	}
	return invalid("body", "request body must be valid JSON")
}

func jsonTypeName(kind reflect.Kind) string {
	switch kind {
	case reflect.Int:
		return "an integer"
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "a boolean"
	case reflect.Slice:
		return "an array"
	default:
		return "an object"
	}
}

// pathID treats anything that cannot be a row id as a row that does not exist.
func pathID(r *http.Request) (int, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 32)
	if err != nil || id <= 0 {
		return 0, store.ErrNotFound
	}
	return int(id), nil
}

func positiveQuery(r *http.Request, name string, fallback int) (int, error) {
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

func paging(r *http.Request) (page, limit int, err error) {
	if page, err = positiveQuery(r, "page", 1); err != nil {
		return 0, 0, err
	}
	if limit, err = positiveQuery(r, "limit", defaultLimit); err != nil {
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

func (s *server) listProducts(w http.ResponseWriter, r *http.Request) error {
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
	search := strings.TrimSpace(q.Get("search"))
	if !storable(search) {
		return invalid("search", "search contains invalid characters")
	}
	products, total, err := s.store.ListProducts(r.Context(), store.ProductFilter{
		Search: search, Status: status, Page: page, Limit: limit,
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, pageResponse[store.Product]{products, page, limit, total})
}

func (s *server) productSummary(w http.ResponseWriter, r *http.Request) error {
	summary, err := s.store.ProductSummary(r.Context())
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
	var errs fieldErrors
	if in.SKU == "" {
		errs.add("sku", "sku is required")
	} else if !storable(in.SKU) {
		errs.add("sku", "sku contains invalid characters")
	}
	if in.Name == "" {
		errs.add("name", "name is required")
	} else if !storable(in.Name) {
		errs.add("name", "name contains invalid characters")
	}
	switch {
	case req.Price == nil:
		errs.add("price", "price is required")
	case *req.Price < 0:
		errs.add("price", "price must be 0 or greater")
	case *req.Price > math.MaxInt32:
		errs.add("price", "price is too large")
	default:
		in.Price = *req.Price
	}
	return in, errs.err()
}

func (s *server) createProduct(w http.ResponseWriter, r *http.Request) error {
	var req productRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	in, err := req.validate()
	if err != nil {
		return err
	}
	product, err := s.store.CreateProduct(r.Context(), in)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, product)
}

func (s *server) updateProduct(w http.ResponseWriter, r *http.Request) error {
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
	product, err := s.store.UpdateProduct(r.Context(), id, in)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, product)
}

func (s *server) deleteProduct(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if err := s.store.DeleteProduct(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

type stockRequest struct {
	Type string `json:"type"`
	Qty  int    `json:"qty"`
	Note string `json:"note"`
}

func (req stockRequest) validate() error {
	var errs fieldErrors
	if req.Type != "in" && req.Type != "out" {
		errs.add("type", `type must be "in" or "out"`)
	}
	if req.Qty <= 0 || req.Qty > math.MaxInt32 {
		errs.add("qty", "qty must be a positive integer")
	}
	if !storable(req.Note) {
		errs.add("note", "note contains invalid characters")
	}
	return errs.err()
}

func (s *server) adjustStock(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var req stockRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	if err := req.validate(); err != nil {
		return err
	}
	var note *string
	if trimmed := strings.TrimSpace(req.Note); trimmed != "" {
		note = &trimmed
	}
	product, err := s.store.AdjustStock(r.Context(), id, req.Type, req.Qty, note)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, product)
}

func (s *server) listMovements(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	movements, err := s.store.ListMovements(r.Context(), id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string][]store.Movement{"data": movements})
}

type orderRequest struct {
	Customer string `json:"customer"`
	Items    []struct {
		SKU string `json:"sku"`
		Qty int    `json:"qty"`
	} `json:"items"`
}

func (req orderRequest) validate(idempotencyKey string) (store.NewOrder, error) {
	order := store.NewOrder{Customer: strings.TrimSpace(req.Customer), IdempotencyKey: idempotencyKey}
	var errs fieldErrors
	if order.Customer == "" {
		errs.add("customer", "customer is required")
	} else if !storable(order.Customer) {
		errs.add("customer", "customer contains invalid characters")
	}
	if len(idempotencyKey) > 255 || !storable(idempotencyKey) {
		errs.add("Idempotency-Key", "Idempotency-Key must be valid text of at most 255 bytes")
	}
	if len(req.Items) == 0 {
		errs.add("items", "items must not be empty")
	}
	seen := map[string]bool{}
	for i, item := range req.Items {
		sku := strings.TrimSpace(item.SKU)
		field := fmt.Sprintf("items[%d]", i)
		switch {
		case sku == "":
			errs.add(field+".sku", fmt.Sprintf("item %d: sku is required", i+1))
		case !storable(sku):
			errs.add(field+".sku", fmt.Sprintf("item %d: sku contains invalid characters", i+1))
		case seen[sku]:
			errs.add(field+".sku", fmt.Sprintf("duplicate SKU %q", sku))
		}
		seen[sku] = true
		if item.Qty <= 0 || item.Qty > math.MaxInt32 {
			errs.add(field+".qty", fmt.Sprintf("item %d: qty must be a positive integer", i+1))
		}
		order.Lines = append(order.Lines, store.OrderLine{SKU: sku, Qty: item.Qty})
	}
	return order, errs.err()
}

func (s *server) createOrder(w http.ResponseWriter, r *http.Request) error {
	var req orderRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	order, err := req.validate(r.Header.Get("Idempotency-Key"))
	if err != nil {
		return err
	}
	created, replayed, err := s.store.CreateOrder(r.Context(), order)
	if err != nil {
		return err
	}
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	return writeJSON(w, http.StatusCreated, created)
}

func (s *server) getOrder(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	order, err := s.store.GetOrder(r.Context(), id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, order)
}

func (s *server) cancelOrder(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	order, err := s.store.CancelOrder(r.Context(), id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, order)
}

func (s *server) listOrders(w http.ResponseWriter, r *http.Request) error {
	page, limit, err := paging(r)
	if err != nil {
		return err
	}
	orders, total, err := s.store.ListOrders(r.Context(), page, limit)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, pageResponse[store.OrderSummary]{orders, page, limit, total})
}

func (s *server) dailySales(w http.ResponseWriter, r *http.Request) error {
	var date *string
	if raw := r.URL.Query().Get("date"); raw != "" {
		if _, err := time.Parse(time.DateOnly, raw); err != nil {
			return invalid("date", "date must be formatted as YYYY-MM-DD")
		}
		date = &raw
	}
	report, err := s.store.DailySales(r.Context(), date)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, report)
}
