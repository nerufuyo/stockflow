# StockFlow

Inventory and sales mini ERP module: Go + PostgreSQL API with a React + TypeScript UI. A warehouse supervisor manages products, adjusts stock and records sales orders that consume stock.

## Run it

Requires Docker with Compose.

```sh
docker compose up --build
```

| What | Where |
| --- | --- |
| App | http://localhost:3000 |
| API | http://localhost:8080 |
| Postgres | `localhost:5433`, user, password and database all `stockflow` |

Migrations and seed data apply automatically on the first start. To start again from a clean database, run `docker compose down -v`.

Host ports can be changed with `FRONTEND_PORT`, `BACKEND_PORT` and `POSTGRES_PORT`. Postgres uses 5433 so it does not collide with a local Postgres on 5432.

### Development mode

Requires Go 1.26+ and Node 22+. Run each command in its own terminal, from the repository root.

```sh
docker compose up -d db
cd backend && go run ./cmd/api      # http://localhost:8080
cd frontend && npm ci && npm run dev # http://localhost:5173
```

The Vite dev server proxies `/api` to `localhost:8080`, and nginx does the same in the Compose setup. The browser therefore always sees one origin, so the backend needs no CORS middleware.

## Tests

The backend tests need Postgres. They create and drop their own `stockflow_test` database, so `stockflow` is untouched.

```sh
docker compose up -d db
cd backend && go test ./...                # needs Go on the host
docker compose run --rm backend-test       # with -race, needs only Docker

cd frontend && npm ci && npm test
```

The backend tests drive the real HTTP handlers against a real database:

- Insufficient stock rolls back everything, including lines that had already been decremented.
- A successful order writes items and movements and updates stock.
- 40 concurrent orders for 10 units produce exactly 10 orders, and stock ends at 0.
- Orders listing the same SKUs in opposite order do not deadlock.
- Two concurrent cancels restore stock once.
- Concurrent retries with one `Idempotency-Key` create one order.
- `stock = SUM(in) - SUM(out)` holds after every scenario and for the seed data.

The frontend tests cover validation, formatting, routing and error-envelope parsing.

## API

The contract in the brief is implemented as specified. Errors share one envelope:

```json
{ "error": { "code": "INSUFFICIENT_STOCK", "message": "Insufficient stock: 3 available", "sku": "ELC-1004", "available": 3 } }
```

`details` lists `{field, message}` pairs on a 400. Error codes are `VALIDATION_ERROR`, `NOT_FOUND`, `INSUFFICIENT_STOCK`, `PRODUCT_HAS_HISTORY`, `SKU_EXISTS`, `SKU_IN_USE`, `ORDER_ALREADY_CANCELLED`, `IDEMPOTENCY_KEY_REUSED` and `INTERNAL_ERROR`.

### Additions to the contract

The UI needs these, and existing behaviour does not change:

| Addition | Why |
| --- | --- |
| `GET /api/products?status=active\|inactive\|low_stock` | Filters must apply across all pages, not only the visible one |
| `GET /api/products/summary` | Summary cards must not be computed from one page of rows |
| `GET /api/orders?page=&limit=` | Without a list, earlier orders cannot be opened or cancelled |
| `Idempotency-Key` on `POST /api/orders` | Bonus. A replay returns the original order and the `Idempotency-Replayed: true` header |
| `GET /api/reports/daily-sales?date=` | Bonus. Days are Asia/Jakarta and cancelled orders are excluded |

### curl examples

```sh
API=http://localhost:8080/api

# List and search
curl "$API/products?search=cable&page=1&limit=5"

# Create, then update (use the id returned by the create call)
curl -X POST $API/products -d '{"sku":"DEMO-1","name":"Demo Item","price":15000}'
curl -X PUT $API/products/1 -d '{"sku":"ELC-1001","name":"USB-C Cable 1m","price":47000}'

# Adjust stock. Overdrawing returns 409 with the available quantity
curl -X POST $API/products/1/stock -d '{"type":"in","qty":10,"note":"Restock"}'
curl -X POST $API/products/1/stock -d '{"type":"out","qty":999999}'
curl $API/products/1/movements

# Delete. Returns 409 PRODUCT_HAS_HISTORY when movements exist
curl -i -X DELETE $API/products/1

# Create an order. Prices and total come from the server
curl -X POST $API/orders -H 'Idempotency-Key: demo-1' \
  -d '{"customer":"PT Maju Jaya","items":[{"sku":"ELC-1002","qty":2},{"sku":"PKG-3001","qty":10}]}'

# Insufficient stock: 409 naming the SKU, and nothing changes
curl -X POST $API/orders -d '{"customer":"PT Maju Jaya","items":[{"sku":"PKG-3003","qty":999}]}'

# Read and cancel. A second cancel returns 409
curl $API/orders/1
curl -X POST $API/orders/1/cancel

# Aggregates
curl $API/products/summary
curl "$API/reports/daily-sales?date=2026-11-06"
```

## Design decisions

**Stock safety.** Stock only changes through a guarded statement such as `UPDATE products SET stock = stock - $2 WHERE sku = $1 AND stock >= $2`. Concurrent transactions queue on the row lock, and each re-evaluates the guard against the committed value, so there is no SELECT-then-UPDATE window. `CHECK (stock >= 0)` is the backstop. The `available` value in a 409 is read only after the guard has already refused.

**Atomic orders.** One transaction covers the order, items, stock decrements and movements. Any failure rolls back all of them, so a failed order leaves no trace except a skipped order id.

**Deadlock avoidance.** An order decrements its products in SKU order, whatever order the client listed them in. Cancelling uses the same order.

**Price integrity.** `unit_price` comes from `RETURNING` on the same `UPDATE` that takes the stock, so price and stock are read under one row lock. Price fields sent by a client are ignored.

**`last_movement_at` is derived.** A correlated subquery reads it from `stock_movements`, served by an index on `(product_id, created_at DESC, id DESC)`. It cannot drift from the ledger and needs no extra write. Maintaining a column would only pay off on far larger lists.

**Movements are the ledger.** Orders and cancellations write movements too (`Order #12`, `Order #12 cancelled`). The product endpoints accept no stock field, so every unit of stock is explained by a movement.

**Deleting relies on foreign keys.** `DELETE` runs directly and a foreign key violation becomes `PRODUCT_HAS_HISTORY`. A check-then-delete could miss a movement inserted in between.

**Schema changes from the brief.**
- `sales_orders.total` is `BIGINT`, because `INTEGER` overflows near Rp 2.1 billion. For the same reason the stock value sum uses `BIGINT`, and JavaScript numbers hold these values exactly.
- `sales_orders` gains `idempotency_key` and `request_hash`. Inserting the order first makes the unique index serialize concurrent retries, so no extra table is needed. A failed attempt rolls back and does not burn the key.
- `sales_order_items` gains `UNIQUE (order_id, sku)` and a `unit_price >= 0` check.

**Renaming a SKU.** A SKU that orders reference cannot change: the update returns 409 `SKU_IN_USE`. Rewriting it would alter order history.

**Behaviour the brief leaves open.**
- Inactive products cannot be ordered (400), but their stock can still be adjusted and cancellations still restore it.
- Low stock means `stock <= 10` regardless of status, and the filter and the card use the same rule.
- Total stock value includes inactive products, because the goods are still in the warehouse.

**Few dependencies.** The backend uses the standard library router (Go 1.22 method patterns) and `pgx`. The frontend uses React only, with a small hash router, a `useAsync` hook that aborts stale requests, and the native `<dialog>` element.

## What I would improve

- Use a real migration tool. The Postgres image only runs the SQL files on an empty volume, so schema changes currently require `docker compose down -v`.
- Add authentication and record who made each movement. Anyone who can reach the API can change stock today.
- Paginate `GET /api/products/{id}/movements`, which returns all history.
- Report every insufficient SKU in one 409. The contract has room for one, so a user with several short lines finds them one at a time.
- Replace the SKU suggestion list, which loads the first 100 active products, with a server-side search.
- Add a `pg_trgm` index if `ILIKE` search becomes slow, and keyset pagination for deep pages.
- Expire old idempotency keys, and add metrics and structured request logs.
- Add browser end-to-end tests to the repository, and CI to run all tests.

## Time spent and how this was built

This was built in one Claude Code (AI) session, so the elapsed time says nothing about how long a person would need, and no human hours were spent writing the code.

Checked during that session, against a local PostgreSQL 18 server (Compose uses 17):

- `go test ./...` passes. Removing the stock guard makes the overselling tests fail, which confirms they can catch the bug.
- `npm test`, `npm run build` and `oxlint` pass.
- A scripted browser run through the Vite dev server passed all 33 checks across every screen, including the 409 and 400 displays, cancelling, and the error state with the backend stopped.

Not yet run, because Docker Desktop on the build machine failed to start:

- `docker compose up --build`, including both Dockerfiles and the nginx proxy. `docker compose config` does validate the file.
- `go test -race`. The race build linked on Windows, but ThreadSanitizer could not allocate its memory there. Use `docker compose run --rm backend-test`.
