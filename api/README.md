# Kart API

A Go server for the food ordering API in [openapi.yaml](openapi.yaml). It also has a coupon-check endpoint. Promo codes are validated by the separate [coupons server](../coupons-service/cmd/coupons-server), which watches the coupon base files.

```
client ──► kart-api :8080 ──HTTP──► coupons-server :8081 ──► coupons-service/data/*.txt
              │  (cached)
              └─ in-memory products & orders (dummy stores)
```

## Run

From the repository root, with `couponbase1.txt`, `couponbase2.txt` and `couponbase3.txt` in `coupons-service/data/`:

```sh
go run ./coupons-service/cmd/coupons-server   # ready after ~18s and ~3 GB peak memory; see /ready
go run ./api/cmd/kart-api
```

```sh
curl localhost:8080/api/product
curl -X POST localhost:8080/api/coupon/validate -H 'Content-Type: application/json' -d '{"couponCode":"HAPPYHRS"}'
curl -X POST localhost:8080/api/order -H 'Content-Type: application/json' -H 'api_key: apitest' \
  -d '{"couponCode":"HAPPYHRS","items":[{"productId":"1","quantity":2}]}'
```

Tests: `go test -short ./...`. `-short` skips the test that loads the real 3 GB coupon files.

## Endpoints

| Method & path | Notes |
|---|---|
| `GET /api/product` | All products |
| `GET /api/product/{productId}` | 400 if the id is not an integer, 404 if no product has it |
| `POST /api/order` | Needs an `api_key` with the `create_order` scope. Rate limited |
| `POST /api/coupon/validate` | Returns `{"couponCode","valid","discountPercent"}`. Rate limited |
| `GET /api/openapi.yaml` | The spec |
| `GET /health` | Liveness |
| `GET /ready` | Readiness. Returns 503 once shutdown begins |

Error bodies use the spec's `ApiResponse` shape: `{"code":422,"type":"validation_error","message":"..."}`.

Order status codes:
- **400**: the body is malformed, has the wrong types, is over 64 KiB, or the Content-Type is not `application/json`.
- **401**: the `api_key` header is missing or unknown.
- **403**: the key lacks the `create_order` scope.
- **422**: validation failed. Every problem is listed, for example an empty `items`, a quantity outside 1–100, an unknown product or an invalid coupon.
- **429**: rate limited.
- **503**: a coupon was given but the coupons server can't be reached. Orders without a coupon still work.

Pricing:
- Money is handled as integer cents.
- A valid coupon takes 10% off the subtotal, rounded half up to the cent.
- `total` is the subtotal minus `discounts`.

Coupon codes:
- A code is valid when it is 8–10 letters or digits and appears in at least two base files.
- Codes match exactly, so `happyhrs` is invalid.

## Configuration

Configuration is read with [Viper](https://github.com/spf13/viper). Each setting comes from an environment variable. `CONFIG_FILE` can optionally name a YAML, JSON or TOML file with the same keys in lower case (`order_rate_limit: 5`). Environment variables override the file, and the file overrides the defaults. The defaults work for local development.

| Variable | Default | |
|---|---|---|
| `ADDR` | `:8080` | Listen address |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` / `json` | `debug`…`error`; `json` or `text` |
| `API_KEYS` | `apitest:create_order` | Comma-separated `key:scope\|scope` entries. A key with no scopes (`readonly:`) gets 403 on orders |
| `COUPONS_URL` / `COUPONS_TIMEOUT` | `http://localhost:8081` / `2s` | Coupons server |
| `COUPON_CACHE_TTL` / `COUPON_NEGATIVE_CACHE_TTL` | `5m` / `1m` | How long valid and invalid answers are cached. `0` disables caching |
| `CACHE_MAX_ENTRIES` | `10000` | LRU bound of the in-memory cache |
| `COUPON_RATE_LIMIT` / `COUPON_RATE_WINDOW` | `20` / `1m` | Per client IP: a burst of 20, refilling at 20 per minute |
| `ORDER_RATE_LIMIT` / `ORDER_RATE_WINDOW` | `10` / `1m` | Per client IP, same scheme |
| `TRUSTED_PROXIES` | (none) | CIDRs of your load balancers. `X-Forwarded-For` is only honoured on connections from them |
| `CORS_ALLOWED_ORIGINS` | `*` | |
| `READ_HEADER_TIMEOUT`, `READ_TIMEOUT`, `WRITE_TIMEOUT`, `IDLE_TIMEOUT`, `SHUTDOWN_TIMEOUT` | `5s`, `10s`, `15s`, `60s`, `15s` | |

Coupons server:

| Variable | Default |
|---|---|
| `COUPONS_ADDR` | `127.0.0.1:8081` (use `:8081` in a container; never expose it publicly) |
| `COUPONS_DIR` | `coupons-service/data` (keep either the `.txt` or the `.gz` files there, never both) |
| `COUPONS_POLL_INTERVAL` | `2s` |
| `COUPONS_DISCOUNT_PERCENT` | `10` |
| `LOG_LEVEL`, `LOG_FORMAT`, `SHUTDOWN_TIMEOUT` | `info`, `json`, `10s` |

## Layout

```
api/
  cmd/kart-api/        wiring, graceful shutdown
  internal/config      env parsing and validation
  internal/httpapi     Echo router, handlers, error handler, middleware (request id,
                       access log, recover, CORS, key auth, rate limits)
  internal/order       validation, pricing, dummy order store
  internal/product     catalogue, dummy product store
  internal/coupon      Validator interface, HTTP client, cache decorator
  internal/cache       Cache interface (Redis-shaped) + in-memory LRU/TTL
  internal/money       integer cents
  internal/logging     slog setup, request id in every log line
```

## Scaling out

The pieces below are in-process and shaped so that each can be swapped on its own:

- **Cache**: implement `cache.Cache` with go-redis (`GET`, `SET … EX`, `DEL`) and pass it to `coupon.NewCached`.
- **Rate limits**: Echo's `RateLimiter` middleware runs on an in-memory token-bucket store. Replace it in `httpapi/ratelimit.go` with a Redis-backed `middleware.RateLimiterStore`, so replicas share their budgets.
- **Stores**: implement `product.Store` and `order.Store` against a database.

Notes for proxies: the spec's header is `api_key`. nginx drops headers containing underscores unless `underscores_in_headers on;` is set.
