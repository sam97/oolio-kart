# Oolio Kart API

[![CI](https://github.com/sam97/oolio-kart/actions/workflows/ci.yml/badge.svg)](https://github.com/sam97/oolio-kart/actions/workflows/ci.yml)

> **This is v2.** It keeps products, orders and coupons in Postgres and builds the valid coupons within a fixed memory cap. The first version, with in-memory stores and a separate coupons HTTP service, is at the [`v1`](https://github.com/sam97/oolio-kart/tree/v1) tag.

> **About AI assistance:** I used Claude while building this project. I have reviewed the code and take full responsibility for it.

A Go implementation of the food-ordering API in [api/kart-api/openapi.yaml](api/kart-api/openapi.yaml): products, orders, and promo codes checked against three large coupon files. It runs as:
- **kart-api:** the public API on port 8080.
- **coupons-job:** works out the valid promo codes from about 3 GB of coupon files and publishes them to Postgres. It builds on start, then rebuilds whenever the files change.
- **migrate:** creates the tables and seeds the product catalogue.
- **Postgres:** holds products, orders and the valid codes.

For the design and the decisions behind it, please see **[ARCHITECTURE.md](ARCHITECTURE.md)**.

## Quick start

You need Docker with Compose v2 and about 1.5 GB of memory and 6 GB of disk for it.

```sh
docker compose up -d --build
```

The first start downloads 2.1 GB of coupon files (about 5 minutes) and builds the valid codes (about 20 s). The API is up at http://localhost:8080 straight away; orders with a coupon answer `503` until the first build is published. `docker compose ps` shows coupons-job as healthy once it is.

Then try it. The API key is `apitest`:

```sh
curl http://127.0.0.1:8080/api/product

curl -X POST http://127.0.0.1:8080/api/coupon/validate \
  -H 'Content-Type: application/json' -d '{"couponCode":"HAPPYHRS"}'

curl -X POST http://127.0.0.1:8080/api/order \
  -H 'Content-Type: application/json' -H 'api_key: apitest' \
  -d '{"couponCode":"HAPPYHRS","items":[{"productId":"1","quantity":2}]}'
```

The order comes to `11.7` instead of `13`: every valid code gives 10% off. `SUPER100` is not a valid code and returns `422`. The full spec is served at http://127.0.0.1:8080/api/openapi.yaml.

<details>
  <summary>Docker commands</summary>

  ```sh
  docker compose ps                                   # what is running and healthy
  docker compose logs -f coupons-job                  # watch a build
  docker compose exec postgres psql -U kart -d kart   # look at the data
  docker compose down                                 # stop; keeps the data
  docker compose down -v                              # stop and delete the data and coupon files
  ```

</details>

### Skipping the download

If you already have `couponbase1.gz`, `couponbase2.gz` and `couponbase3.gz`, please either copy them into `data/seed/`, or point Compose at their folder in a `.env` file next to `docker-compose.yml`:

```sh
COUPONS_SEED_DIR=C:/Users/me/Downloads
```

## Run without Docker

You need Go 1.27.1 or later and a Postgres server; `docker compose up -d postgres` starts one on `localhost:5432`. Put the three coupon files in `data/coupons/`, either as `.gz` or unzipped as `.txt`, but not both, or every code counts as appearing twice. Then, from the repository root:

```sh
go run ./cmd/migrate        # once, and after pulling new migrations
go run ./cmd/coupons-job    # builds, then checks the files every minute; -once builds and exits
go run ./api/kart-api       # http://localhost:8080
```

## Configuration

Every program reads its defaults from [.env.defaults](.env.defaults), so nothing needs setting up to run locally. To change a setting, please override it rather than editing that file:
- **Local development:** put the keys you want to change in a `.env` file in the repository root. It is gitignored.
- **Staging or production:** point `ENV_FILE` at an env file, e.g. `ENV_FILE=deploy/staging.env docker compose up -d`.
- **A single setting:** set the environment variable of the same name.

The database credentials in `.env.defaults` and `docker-compose.yml` are for local development only; please set `DATABASE_URL` from a secret anywhere else. The discount and the coupon rules live in the database, in `coupon_settings`. For every setting, and for the alternative ClickHouse and Pebble coupon scanners, please refer to [ARCHITECTURE.md](ARCHITECTURE.md#configuration).

## Tests

```sh
go test -short ./...
```

Tests that need a database are skipped unless `TEST_DATABASE_URL` is set. Each runs in its own schema, dropped afterwards, so the Compose database is safe to use:

```sh
docker compose up -d postgres
TEST_DATABASE_URL='postgres://kart:kart@localhost:5432/kart?sslmode=disable' go test -short ./...
```

Without `-short`, the scanners also run their stress cases (a few minutes), and one test builds the codes from the real files in `data/coupons/` while checking memory stays under the cap:

```sh
go test -v -run TestBaseFiles ./pkg/services/coupons
```

CI ([.github/workflows/ci.yml](.github/workflows/ci.yml)) checks formatting, runs `go vet` and `go test -race -short` against a Postgres service, and builds the Docker images.
