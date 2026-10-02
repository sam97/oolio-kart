# Kart API

A Go server for the food ordering API in [openapi.yaml](api/openapi.yaml). It also has a coupon-check endpoint. Promo codes are validated by the separate [coupons server](cmd/coupons-server), which watches the coupon base files.

```
client ──► kart-api :8080 ──HTTP──► coupons-server :8081 ──► data/coupons/*.txt
              │  (cached)
              └─ in-memory products & orders (dummy stores)
```

## Run with Docker

```sh
docker compose up -d --build
```

This starts three containers:
- **`coupon-data`** runs once. It fills the `coupons` volume with `couponbase1.gz`, `couponbase2.gz` and `couponbase3.gz`, about 2.1 GB in total, and verifies each one.
  - By default it downloads them from S3.
  - To use copies you already have, put them in `data/seed/`, or set `COUPONS_SEED_DIR` in a `.env` file to the folder that holds them, for example `COUPONS_SEED_DIR=C:/Users/me/Downloads`.
  - Files already in the volume are kept.
- **`kart-api`** is published on port 8080. It does not wait for the coupons server: it answers straight away, and orders without a coupon work while coupons are loading or down.
- **`coupons-server`** is reachable only from `kart-api`.
  - The first start builds the codes from the gzip files, which takes about 30 s and peaks near 3 GB. It runs under a 4 GB memory limit, so Docker needs at least 6 GB of memory.
  - Later starts restore the saved codes from the volume and are ready in a few seconds.

Both images are distroless, run as non-root and have read-only root filesystems. The containers' health checks run the binary itself with `-healthcheck`, which GETs `/ready`. `docker compose down -v` also deletes the coupons volume, so the next start copies or downloads the files again.

## Run without Docker

From the repository root, with `couponbase1.txt`, `couponbase2.txt` and `couponbase3.txt` in `data/coupons/`:

```sh
go run ./cmd/coupons-server   # first start: ready after ~18s and ~3 GB peak memory; see /ready
go run ./cmd/kart-api
```

After a successful load the coupons server saves the valid codes to `.valid-codes.json` in the coupon folder. A restart with the same files (same names, sizes and modification times) restores them and is ready immediately. Any change to the files triggers a full rebuild, and a missing or corrupt saved file is ignored.

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
| `COUPONS_DIR` | `data/coupons` (keep either the `.txt` or the `.gz` files there, never both. If it is read-only, saving the codes is skipped with a warning) |
| `COUPONS_POLL_INTERVAL` | `2s` |
| `COUPONS_DISCOUNT_PERCENT` | `10` |
| `LOG_LEVEL`, `LOG_FORMAT`, `SHUTDOWN_TIMEOUT` | `info`, `json`, `10s` |

## Layout

```
api/                    OpenAPI document, embedded and served at /api/openapi.yaml
cmd/kart-api/           API wiring, graceful shutdown
cmd/coupons-server/     coupons server config and wiring
internal/config         kart-api env parsing and validation
internal/httpapi        Echo router, handlers, error handler, middleware (request id,
                        access log, recover, CORS, key auth, rate limits)
internal/order          validation, pricing, dummy order store
internal/product        catalogue, dummy product store
internal/couponclient   Validator interface, HTTP client, cache decorator
internal/cache          Cache interface (Redis-shaped) + in-memory LRU/TTL
internal/money          integer cents
internal/logging        slog setup, request id in every log line
internal/coupons        loads the coupon base files and keeps the valid codes in sync
internal/couponsapi     coupons server HTTP handlers
internal/healthcheck    the -healthcheck probe used by the container health checks
data/coupons/           coupon base files for running without Docker (gitignored)
data/seed/              optional local .gz copies for docker compose (gitignored)
scripts/                fetch-coupons.sh, run by the coupon-data container
Dockerfile              one image per binary, chosen with --build-arg CMD
docker-compose.yml      coupon-data, coupons-server, kart-api
```

## Scaling out

The pieces below are in-process and shaped so that each can be swapped on its own:

- **Cache**: implement `cache.Cache` with go-redis (`GET`, `SET … EX`, `DEL`) and pass it to `couponclient.NewCached`.
- **Rate limits**: Echo's `RateLimiter` middleware runs on an in-memory token-bucket store. Replace it in `httpapi/ratelimit.go` with a Redis-backed `middleware.RateLimiterStore`, so replicas share their budgets.
- **Stores**: implement `product.Store` and `order.Store` against a database.

Notes for proxies: the spec's header is `api_key`. nginx drops headers containing underscores unless `underscores_in_headers on;` is set.
