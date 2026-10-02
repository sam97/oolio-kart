# Oolio Kart API

> **About AI assistance:** I used Claude while building this project. I have reviewed the code and take full responsibility for it.

## The assignment

The [Oolio advanced backend challenge](https://github.com/oolio-group/kart-challenge/tree/advanced-challenge/backend-challenge) asks for an API server in Go that:
- implements every API in the food-ordering OpenAPI 3.1 spec, conforming to it as closely as possible
- implements every feature of the demo API server, which is no longer reachable, so this implementation follows the spec
- validates promo codes: a code is valid if it is 8–10 characters long and appears in at least two of the three coupon base files (`couponbase1.gz`, `couponbase2.gz`, `couponbase3.gz`)
- is robust, using best judgement for the edge cases the demo server leaves out

> **Discount:** the brief defines which codes are valid but not what discount a valid code gives. This implementation therefore applies a flat 10% off the subtotal for every valid code. The percentage can be changed with `COUPONS_DISCOUNT_PERCENT` on the coupons server, and per-coupon rules could be added later (see [ARCHITECTURE.md](ARCHITECTURE.md#how-kart-api-uses-it-internalcouponclient)).

## This implementation

A Go implementation of the Oolio food-ordering API ([api/openapi.yaml](api/openapi.yaml)). It includes promo-code validation against the three coupon base files. The work is split into two services:
- **kart-api:** the public API for products, orders and the coupon check.
- **coupons-server:** an internal service that works out the valid codes from about 3 GB of coupon files. Only 8 codes turn out to be valid.

For the design, how coupons are computed, and other technical decisions please see **[ARCHITECTURE.md](ARCHITECTURE.md)**.

## System requirements

**With Docker (recommended)**
- Docker Engine 25 or later with Compose v2 (Docker Desktop 4.27 or later).
- At least **6 GB of memory** available to Docker. The coupons server's first build peaks near 3 GB and runs under a 4 GB limit. On Docker Desktop, set this under Settings → Resources.
- About 3 GB of free disk for the coupon files (2.1 GB of `.gz`) and the images (about 40 MB).
- Internet access on the first run: the stack downloads 2.1 GB of coupon files, which took about 5 minutes when tested. See [Using local copies](#using-local-copies-of-the-coupon-files) to skip this.
- Port 8080 free.

**Without Docker**
- Go 1.27.1 or later.
- About 3 GB of free memory for the coupons server's first build.
- The three coupon files, either as `.gz` (2.1 GB) or unzipped as `.txt` (3.1 GB).
- Ports 8080 and 8081 free.

## Run with Docker

```sh
docker compose up -d --build
```

This starts three containers:
1. **coupon-data** runs once and downloads the coupon files into a Docker volume.
2. **coupons-server** builds the valid codes, taking about 30 s the first time. It reports healthy when it's ready.
3. **kart-api** listens on http://localhost:8080 and is usable straight away. Orders that include a coupon get `503` until coupons-server is ready.

Later starts reuse the volume. coupons-server restores its saved results and is ready in a few seconds.

<details>
<summary>Docker commands</summary>
```sh
docker compose ps                         # both services show "healthy" when ready
docker compose logs -f coupons-server     # watch the first build
docker compose down                       # stop; keeps the coupon volume
docker compose down -v                    # stop and delete the coupon volume
```
</details>

### Using local copies of the coupon files

If you already have `couponbase1.gz`, `couponbase2.gz` and `couponbase3.gz`, you can skip the download in either of two ways:
- Copy them into `data/seed/`.
- Point Compose at their folder in a `.env` file next to `docker-compose.yml`:

  ```sh
  COUPONS_SEED_DIR=C:/Users/me/Downloads
  ```

Only the `.gz` files are used here.

## Run without Docker

Put the three coupon files in `data/coupons/`. The `.gz` files can be copied in as they are, or unzipped to `.txt` for a faster first build (about 18 s instead of about 30 s). Use one format only: if both are there, every code counts as appearing in two files. Then start each service in its own terminal from the repository root:

```sh
go run ./cmd/coupons-server   # first start: ready after ~18-30 s (watch /ready on :8081)
go run ./cmd/kart-api         # http://localhost:8080
```

## Try it

The API key is `apitest`. These examples use `127.0.0.1` because on some machines `localhost` resolves to IPv6 first.

```sh
curl http://127.0.0.1:8080/api/product
curl http://127.0.0.1:8080/api/product/1

curl -X POST http://127.0.0.1:8080/api/coupon/validate \
  -H 'Content-Type: application/json' -d '{"couponCode":"HAPPYHRS"}'

curl -X POST http://127.0.0.1:8080/api/order \
  -H 'Content-Type: application/json' -H 'api_key: apitest' \
  -d '{"couponCode":"HAPPYHRS","items":[{"productId":"1","quantity":2}]}'
```

With `HAPPYHRS`, the last order comes to `11.7` instead of `13` (10% off). `SUPER100` returns `422`. The full spec is served at http://127.0.0.1:8080/api/openapi.yaml.

## Tests

```sh
go test -short ./...
```

`-short` skips one test that builds the codes from the real files in `data/coupons/`. That test takes about 18 s and about 3 GB of memory:

```sh
go test -v -run TestBaseFiles ./internal/coupons
```

CI ([.github/workflows/ci.yml](.github/workflows/ci.yml)) checks formatting, then runs `go vet` and `go test -race -short`, and builds both Docker images.
