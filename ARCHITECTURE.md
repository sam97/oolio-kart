# Architecture

This describes v2. The first version, with in-memory stores and a separate coupons HTTP service, is at the [`v1`](https://github.com/sam97/oolio-kart/tree/v1) tag.

Three Go programs share one Postgres database, run together with Docker Compose:

- **kart-api** is the public API from [api/kart-api/openapi.yaml](api/kart-api/openapi.yaml). It serves products, places orders and checks coupons, all from Postgres.
- **coupons-job** works out which promo codes are valid from the three coupon base files, and publishes them to Postgres. It builds on start, then checks the files on a schedule.
- **migrate** creates and updates the tables, and seeds the product catalogue.

The coupon build and the API are kept apart because their needs are very different. A build reads about 3 GB of text and is CPU-heavy for 10–20 s; the API needs a few MB and answers in milliseconds. Keeping them apart means:
- a build can't slow the API down or run it out of memory
- either program can be redeployed or scaled on its own
- the API never waits for a build: it reads whatever set was last published

## Contents

- [Diagram](#diagram)
- [Code layout and data stores](#code-layout-and-data-stores)
- [Coupons](#coupons)
  - [The rule](#the-rule)
  - [What the data looks like](#what-the-data-looks-like)
  - [How the valid set is built](#how-the-valid-set-is-built)
  - [Memory budget](#memory-budget)
  - [How a build is published](#how-a-build-is-published)
  - [Coupon settings](#coupon-settings)
  - [How kart-api checks a code](#how-kart-api-checks-a-code)
  - [When things fail](#when-things-fail)
- [Products and orders](#products-and-orders)
- [Decisions](#decisions)
  - [Made for production](#made-for-production)
  - [Made only for this assignment](#made-only-for-this-assignment)
- [API notes](#api-notes)
- [Configuration](#configuration)
- [Where things are](#where-things-are)
- [Production scope](#production-scope)

## Diagram

```mermaid
flowchart LR
    ui([Client / UI]) -->|HTTP :8080| router

    subgraph kart["kart-api"]
        router["Echo router<br/>request ID · logs · CORS<br/>API key · rate limits"]
        products["Products service"]
        orders["Orders service<br/>validation · pricing in cents"]
        validator["Coupon validator"]
        cache[("Memory cache<br/>LRU + TTL")]

        router --> products
        router --> orders
        router -->|POST /api/coupon/validate| validator
        orders --> validator
        validator --> cache
    end

    subgraph db["Postgres"]
        catalogue[("products")]
        orderrows[("orders · order_items")]
        codes[("coupon_codes · coupon_manifest<br/>coupon_settings")]
    end

    products --> catalogue
    orders --> catalogue
    orders --> orderrows
    cache -->|miss| codes

    subgraph job["coupons-job"]
        schedule["Schedule<br/>on start, then @every 1m"]
        scanner["Bucket scanner<br/>memory-capped"]
        schedule --> scanner
    end

    scanner --> files[("couponbase1–3<br/>.txt or .gz")]
    scanner <--> buckets[("bucket files<br/>on disk")]
    scanner -->|one transaction| codes
    migrate["migrate"] -->|schema + seed| db
```

<details>
<summary>Same diagram in plain text</summary>

```text
  Client / UI
       |  HTTP :8080
       v
+-----------------------------------------------------------+
| kart-api                                                  |
|   Echo router: request ID . logs . CORS . API key . rates |
|      |               |                  |                 |
|      v               v                  v                 |
|   Products       Orders service ---> Coupon validator     |
|   service        (pricing, cents)        |                |
|      |               |              Memory cache (LRU)    |
+------|---------------|-------------------|----------------+
       v               v                   v  (on a miss)
+-----------------------------------------------------------+
| Postgres                                                  |
|   products    orders, order_items    coupon_codes,        |
|                                      coupon_manifest,     |
|                                      coupon_settings      |
+-----------------------------------------------------------+
       ^ schema + seed                     ^ one transaction per build
       |                                   |
   migrate                    +---------------------------+
                              | coupons-job               |
                              |   on start, then @every 1m|
                              |   bucket scanner          |
                              +---------------------------+
                                  |               ^ v
                           couponbase1-3     bucket files
                           (.txt or .gz)     (on disk)
```

</details>

How an order with a coupon is handled:
1. The router assigns a request ID and checks the `api_key` and the rate limit.
2. The orders service validates the items and looks up all their products in one query.
3. It asks the coupon validator about the code, which answers from the memory cache or from Postgres.
4. The order is priced in integer cents, given a time-ordered UUIDv7 ID, and saved with its items in one transaction.

## Code layout and data stores

Business logic lives in `pkg/services`; it only talks to data through interfaces in `pkg/datasources/<domain>store` and `pkg/datasources/couponsource`. The implementations — Postgres, the memory cache and the coupon file reader — live in [pkg/datasources/internal/datastores](pkg/datasources/internal/datastores). Go's `internal` rule means only code under `pkg/datasources` can import them, so no service, handler or `main` package can depend on Postgres directly.

The binaries get their stores from one factory, [`datasources.Open`](pkg/datasources/datasources.go):

```go
stores, err := datasources.Open(ctx, datasources.Config{DatabaseURL: ..., Cache: ...})
orders := orders.NewService(stores.Products, stores.Orders, coupons)
```

The database URL's scheme picks the implementation, and the cache settings decide whether coupon lookups go through the memory cache. Swapping Postgres, or putting Valkey in front of it, changes `pkg/datasources` only. `datasources.CouponFiles(dir)` likewise hides how the coupon files are read.

## Coupons

### The rule
A promo code is valid if it is 8–10 characters long and appears in at least two of `couponbase1`, `couponbase2` and `couponbase3`. The lengths and the number of files are [settings in the database](#coupon-settings).

### What the data looks like
The three files hold 313 million lines in total, about 3.1 GB uncompressed:

| File | Lines | Contents |
|---|---|---|
| couponbase1 | 107,260,777 | All 8 characters, with 2,077 duplicate lines |
| couponbase2 | 107,260,776 | 9 characters, plus 8 eight-character lines at the end |
| couponbase3 | 98,566,152 | 10 characters, plus 8 eight-character lines at the end |

**Only 8 codes are valid:** `BIRTHDAY`, `BUYGETON`, `FIFTYOFF`, `FREEZAAA`, `GNULINUX`, `HAPPYHRS`, `OVER9000`, `SIXTYOFF`.

- `HAPPYHRS` is in files 1 and 3 only. The rest are in more files.
- `SUPER100` (file 1 only) and `MOODYHRS` (file 2 only) are decoys.
- `HAPPYHOURS` and `BUYGETONE`, from the front-end brief, are not valid under these rules.

The code doesn't take shortcuts from this layout. For example, it doesn't just compare the eight-character tail lines. It computes the answer for any input files, so new files would still give a correct result.

### How the valid set is built
([pkg/services/coupons/scanner](pkg/services/coupons/scanner))

The requirement is to find codes that appear in at least two of three files. There are 313 million lines, 3.1 GB of text, and a discount that costs real money whenever a code is wrongly accepted. The decisions below were taken in this order, each one prompted by the limits of the one before it.

- Probabilistic filters were considered first. A Bloom filter per file would answer "is this code in file 2?" in a few hundred MB, but false positives are inherent to it, and a wrongly accepted code is a free discount. No error rate above zero was considered acceptable for a check that affects revenue.

  ```text
  Bloom filter of file 2:   bits  0 1 0 1 1 0 1 0
  "HAPPYHRS" hashes to bits 1, 3, 6  →  all set  →  "probably in file 2"   (correct)
  "SUPER100" hashes to bits 3, 4, 6  →  all set  →  "probably in file 2"   (wrong: set by other codes)
  ```

- Counting structures, such as counting Bloom filters and count-min sketches, are closer to the question, since they count occurrences. They were ruled out for the same reason: they remain probabilistic, as codes that share a counter inflate each other's counts.
- Heaps were explored next, but a working solution built on them was not found.
- An exact approach was therefore adopted: each file's codes are sorted and de-duplicated, then the files are merged and every code is counted. The de-duplication is essential, as file 1's 2,077 duplicate lines would otherwise become false positives. Held as strings, however, 313 million codes need tens of GB in a map, and about 8 GB even when sorted.

  ```text
  file 1: SUPER100 HAPPYHRS SUPER100   sort, de-duplicate →  HAPPYHRS SUPER100
  file 2: FIFTYOFF                                       →  FIFTYOFF
  file 3: HAPPYHRS FIFTYOFF                              →  FIFTYOFF HAPPYHRS
  merge and count:  FIFTYOFF 2 ✓   HAPPYHRS 2 ✓   SUPER100 1   (repeated in file 1, but one file)
  ```

- To shrink the codes, each one is packed into a `uint64`. A code has at most 10 characters from 62 symbols, so 6 bits per character fit in 60 bits ([codec](pkg/services/coupons/codec)); short codes are padded so that integer order equals string order. At 8 bytes per code, every line fits in about 2.5 GB and can be sorted with `slices.Sort`. This was v1, which took 19.5 s from `.txt` and peaked at about 2.6 GB.

  ```text
  "HAPPYHRS"  →  H=18  A=11  P=26  P=26  Y=35  H=18  R=28  S=29  pad pad
                 010010 001011 011010 011010 100011 010010 011100 011101 000000 000000   (60 of 64 bits)

  symbols: 0 = padding, then 0–9, A–Z, a–z in ASCII order, so integer order is string order
  ```

- Memory still grew with the input, and twice the files would have needed twice the memory, so the sorting was moved to disk, where memory stays fixed whatever the input size. An NVMe SSD is preferred: on a SATA hard disk, the same build took over 90 s.
- The codes are split into buckets small enough to be sorted in memory. Bucket `i` holds every code whose hash is `i` mod `P`, so every copy of a code, in every file, lands in the same bucket, and each bucket can be counted on its own. `P` is chosen so that one bucket, across all files, fits one worker's memory slot with 15% headroom: 191 buckets of about 13 MB for the real files.

  ```text
  hash(HAPPYHRS) % 4 = 2      file 1 → bucket 2     file 3 → bucket 2
  hash(FIFTYOFF) % 4 = 0      file 2 → bucket 0     file 3 → bucket 0

  bucket 0: { file 2: FIFTYOFF · file 3: FIFTYOFF }  ← counted alone, never needs bucket 2
  bucket 2: { file 1: HAPPYHRS · file 3: HAPPYHRS }
  ```

- The buckets must also be spread evenly. Codes are not random, as they share prefixes and lengths, so a plain modulus would crowd some buckets. The bucket is therefore `splitmix64(code) % P`, a hash finaliser that mixes every input bit into every output bit. Should a bucket still outgrow its slot, it falls back to an external merge sort in runs, so the memory cap holds regardless; this is verified in the tests with a deliberately poor identity hash.

  ```text
  code % 4, codes sharing a suffix:    b0 ████████████  b1 ░  b2 ░  b3 ░    one bucket overflows
  splitmix64(code) % 4:                b0 ███  b1 ███  b2 ███  b3 ███       all fit their slot
  ```

- Each file is given its own set of buckets rather than a shared one. This costs nothing today, and it allows a file to be added, removed or updated later without the others being touched.

  ```text
  .buckets/
    couponbase1.gz-b7cdd9e8/   b000.dat  b001.dat … b190.dat
    couponbase2.gz-bc26534b/   b000.dat  b001.dat … b190.dat
    couponbase3.gz-caa163a2/   b000.dat  b001.dat … b190.dat
                               └── bucket 0 of every file is counted together
  ```

- The memory each goroutine needs is calculated up front, so that worker counts are as high as the budget allows and no higher. A single setting, `COUPONS_MEMORY_LIMIT`, is divided between the stages, and each stage turns its share into buffer sizes and worker counts (see [Memory budget](#memory-budget)). The CPU-bound workers are capped at one per core. This is a conservative choice: more workers could certainly be assigned to each core, but they would wait for each other and interleave their work, and too many would increase I/O.

  ```text
  256 MiB, 16 cores:   16 encode workers × ~2 MiB     573 bucket writers × ~86 KiB
                       12 count workers × 16 MiB slots
  ```

- The codes are scattered into the buckets. One reader per file streams pooled chunks of whole lines; gzip is detected by its magic bytes and decompressed with `klauspost/pgzip` ([files](pkg/datasources/internal/datastores/files)). Encode workers pack each line, group a chunk's codes by bucket with a counting sort, and append each group to its bucket file.

  ```text
  chunk of file 1:  HAPPYHRS SUPER100 BIRTHDAY …
  pack + bucket:    (h1, b2) (s1, b0) (b1, b2) …
  counting sort:    b0: [s1 …]   b2: [h1 b1 …]        →  append to couponbase1/b000.dat, b002.dat
  ```

- Each bucket is then merge-counted across the files. Count workers take one bucket at a time: each file's part is sorted, de-duplicated and written back sorted, then a k-way merge counts the number of files in which each code appears. Every code found in at least `min_files` files is passed straight to the store, which copies the codes into Postgres and swaps them in within one transaction (see [How a build is published](#how-a-build-is-published)).

  ```text
  bucket 2:   file 1: 9 4 7 4  →  4 7 9         merge of each file's next code:
              file 2: 7 2      →  2 7           2 (1 file)  4 (2 files ✓)  7 (2 files ✓)
              file 3: 4 1      →  1 4           9 (1 file)  1 (1 file)   →  valid: 4, 7
  ```

  The scatter and the count are animated below with 3 files and 4 buckets ([MP4 version](docs/animations/scanner-goroutines.mp4)):

  ![The bucket scanner's goroutines: readers, encode workers scattering into bucket files, and count workers](docs/animations/scanner-goroutines.gif)

- The state of each published build is persisted in a manifest: every file's name, size and modification time, the rules, the scanner, and the bucket layout. A run whose files and rules match the manifest does nothing, and the bucket files left on disk are described well enough to be reused.

  ```text
  coupon_manifest
    rules:   alphanumeric:8-10;minFiles=2;scanner=go
    sources: couponbase1.gz 655069725 B 2026-10-02T05:13 · couponbase2.gz … · couponbase3.gz …
    layout:  splitmix64 · 191 buckets · little-endian · codes per file
  next run:  same files and rules?  yes → skip     no → build
  ```

- The design was kept modular throughout. Reading, decompressing, encoding, scanning and storing each sit behind an interface, so any one of them can be replaced; `COUPONS_SCANNER` selects the scanner, and every scanner must pass the same edge and stress test suite ([scannertest](pkg/services/coupons/scanner/scannertest)).

  ```text
  couponsource.Reader ─▶ Decompressor ─▶ codec.Codec ─▶ scanner.Scanner ─▶ couponstore.Batch
    folder of files       gzip            6-bit packing    go | clickhouse     Postgres
                                                           | pebble
  ```

- That modularity is what made it possible to evaluate two established engines as drop-in scanners, with no change to the rest of the job:
  - **ClickHouse** ([clickhouse](pkg/services/coupons/scanner/clickhouse)): the server reads the files itself with `file()`, loads the codes into a MergeTree table, and finds the valid ones with `GROUP BY code HAVING uniqExact(file) >= min_files`. A separate server is required, and its memory is capped per query rather than by `COUPONS_MEMORY_LIMIT`.
  - **Pebble** ([pebble](pkg/services/coupons/scanner/pebble)), CockroachDB's key-value store: each line becomes a key, the packed code followed by the file's index, so sorting and de-duplication are provided by the store. Pebble's normal write path cost about 40 bytes per key in memtables and took 19 minutes, so codes are instead written as sorted runs, ingested as sstables, and walked in parallel key ranges.

  ```text
  Pebble keys (code ‖ file):   FIFTYOFF‖2  FIFTYOFF‖3  HAPPYHRS‖1  HAPPYHRS‖3  SUPER100‖1
                               └─ 2 files ✓ ─┘          └─ 2 files ✓ ─┘          1 file
  ```

All three scanners find the same 8 codes, but neither alternative outperforms the bucket scanner, which avoids a database's per-key overhead altogether. The figures below were measured on the real files in Docker Desktop (16 CPUs, files in Docker volumes on an NVMe SSD), with `COUPONS_MEMORY_LIMIT=256MiB`:

| Scanner | Input | Total | Phase 1 (scatter / load) | Phase 2 (count / scan) | Peak memory |
|---|---|---|---|---|---|
| **go** (buckets) | `.txt` | **8.2 s** | 4.3 s | 3.8 s | **188 MiB** |
| **go** (buckets) | `.gz` | **11.8 s** | 8.0 s | 3.8 s | **211 MiB** |
| pebble | `.txt` | 49.6 s | 33.7 s | 15.3 s | 227 MiB |
| pebble | `.gz` | 56.2 s | 40.4 s | 15.1 s | 231 MiB |
| clickhouse | `.txt` | 102.7 s | 86.8 s | 14.8 s | 748 MiB per query; 3.8 GB server |
| clickhouse | `.gz` | 97.8 s | 81.0 s | 15.7 s | 746 MiB per query; 3.9 GB server |

- Memory for go and Pebble is the Go runtime's peak, sampled every millisecond. For ClickHouse, it is the largest query's peak from `system.query_log`, and the server container's peak in `docker stats`, which includes its caches and background merges.
- ClickHouse's load is limited to 4 threads and 1 GB per query, so that it fits a modest server; it would load faster with more of both.
- Pebble spends most of its time building and ingesting sorted runs: the same sorting work as the bucket scanner, plus a key-value store's per-key bookkeeping.

Incremental builds have been planned but are not implemented yet; today, any change to the files rebuilds everything. Under the plan, an added, removed or changed file would rebuild only its own buckets, and the affected buckets would then be recounted from the sorted parts on disk. The bucket count would stay fixed between builds, so buckets would grow as files are added, and a full rebuild would be triggered once a bucket reached 4× the size it was planned for.

```text
couponbase2.gz changes:   rescatter couponbase2 only  →  recount every bucket from the sorted parts on disk
couponbase4.gz added:     scatter couponbase4 only    →  buckets grow by a third: still under 4×, keep P
files grow 4×:            a bucket passes 4× its planned size  →  re-plan P, full rebuild
```

### Memory budget
([pkg/services/coupons/budget.go](pkg/services/coupons/budget.go))

`COUPONS_MEMORY_LIMIT` is the only memory setting; every buffer size and worker count is derived from it. A quarter is held back for garbage, goroutine stacks and the runtime. The other three quarters are spent twice, because the two phases never run at the same time:

```text
cap 256 MiB
├─ reserve ¼   64 MiB
└─ work    ¾  192 MiB
   scatter: read files, write buckets           count: sort buckets, find valid codes
   ├─ Reader     48 MiB  decompressors, chunks   └─ Count 192 MiB  one slot per worker
   ├─ Encoders   48 MiB  encode workers
   ├─ Buckets    48 MiB  bucket write buffers
   └─ slack      48 MiB  short-lived garbage
```

The process also sets Go's memory limit to the cap, unless `GOMEMLIMIT` is set.

### How a build is published
([pkg/services/coupons/job.go](pkg/services/coupons/job.go))

Each run of coupons-job:
1. takes a Postgres advisory lock, so two runs never build at once (a crashed holder releases it when its connection drops)
2. reads the [coupon settings](#coupon-settings) and lists the files
3. skips the build if the published manifest was built from the same files (name, size, modification time) and rules
4. otherwise scans the files into a batch, inside a transaction
5. commits: the old codes are deleted, the new ones inserted and the manifest replaced, all in one transaction

Readers see the old set or the new one, never a mix. If the job is killed mid-build, nothing is published and the previous set keeps being served.

The job runs once on start, then on `COUPONS_SCHEDULE` (default `@every 1m`). Checking unchanged files costs one folder listing and one query. `-once` runs a single build for a Kubernetes CronJob or cron, and `-healthcheck` passes once a set is published. Docker can't trigger a container when files in a volume change, and file-change events are unreliable on Docker Desktop mounts, so checking on a schedule is the dependable trigger. Once the files live in blob storage, an event such as an S3 upload notification could trigger the job instead, so a build starts as soon as a file changes.

### Coupon settings
The `coupon_settings` table has one row: `min_length`, `max_length`, `min_files` and `discount_percent`, seeded as 8, 10, 2 and 10. The table rejects values the build can't handle, such as a length over 10.

- Changing the lengths or `min_files` changes the build's rules, so the next run rebuilds.
- Changing `discount_percent` needs no build. kart-api picks it up within `COUPON_SETTINGS_CACHE_TTL` (1 minute).
- Only one coupon type exists today, but the table can be extended to define several, each with its own rules and business logic, such as a different discount percentage.

```sql
UPDATE coupon_settings SET discount_percent = 15;
```

### How kart-api checks a code
([pkg/services/coupons/validator](pkg/services/coupons/validator))

- **Format check first:** a code that isn't 1–10 letters or digits is rejected without a lookup, and one outside the configured lengths is invalid too.
- **Lookup:** one query asks whether the code is published and whether any set has been published yet. Before the first build, answers are "unavailable" rather than "invalid".
- **Caching:** whether a code is valid is cached for 5 minutes, and invalid answers for 1 minute. "Unavailable" is never cached. The discount always comes from the settings, so a cached code still gets the current discount. `Stores.BustCache` empties the cache.
- **Exact match:** codes are case-sensitive, so `happyhrs` is invalid.
- **Discount:** taken off the subtotal and rounded half up to the cent.

### When things fail

| Situation | Behaviour |
|---|---|
| No build published yet, order has no coupon | Normal 200 |
| …order has a coupon | **503** with `Retry-After: 5`. A discount is never silently dropped or granted unchecked |
| Postgres down | Products and orders answer **503** with `Retry-After: 5`; coupon answers still in the cache keep working. `/ready` stays 200, so a short outage doesn't pull the API out of rotation |
| Coupon files change | Rebuilt on the next scheduled check; the old set is served meanwhile |
| Files change during a build | That build is thrown away and the next run builds again |
| A build fails or is killed | Nothing is published; the previous set is kept |
| kart-api receives SIGTERM | `/ready` turns 503, then requests drain for up to 15 s |

## Products and orders

([pkg/datasources/internal/datastores/postgres](pkg/datasources/internal/datastores/postgres))

- **products:** the catalogue, seeded with the 9 demo products by a migration. IDs are integers in the table, as in the spec.
- **orders:** one row per order, with the totals in cents and the coupon code, if any.
- **order_items:** one row per item, in the order given, with the unit price at the time of the order, so past orders keep their prices if the catalogue changes.

An order and its items are written in one transaction. Migrations are embedded SQL files run by goose from `cmd/migrate`; kart-api starts only after they have run.

## Decisions

### Made for production

These choices would stay as they are in a real deployment.

| Decision | Why |
|---|---|
| **Go 1.27** | The brief's suggested language. One static binary per program, cheap concurrency for the coupon build, and a strong standard library (`log/slog`, `net/http`, `slices`). |
| **Echo v5** | Built-in middleware for request ID, structured access logs, recover, CORS, key auth and rate limiting. One central error handler renders the spec's `ApiResponse` shape, and graceful start/stop is built in. |
| **Viper** | 12-factor configuration: defaults in a committed `.env.defaults`, layered override files, environment variables on top, and validation at startup. |
| **Postgres, with pgx and goose** | One durable store for products, orders and coupons. Transactions make a coupon build appear all at once and keep an order with its items. |
| **Data stores behind `internal`** | Services can't reach past the interfaces, so changing a store, or adding a cache in front of one, touches only `pkg/datasources`. |
| **Coupon build as a scheduled job** | The heavy work is kept off the request path, and the API reads a published result instead of calling another service. |
| **Memory-capped bucket scanner** | Memory is set by configuration, not by the size of the files: the same build runs in 64 MiB or 1 GiB, only faster or slower. |
| **Memory cache, hashicorp/golang-lru with TTLs** | Repeated checks don't hit the database. It is bounded, and invalid answers expire sooner than valid ones. |
| **Money in integer cents** | No floating-point rounding errors. Floats appear only in the JSON the spec requires. |
| **API key scopes, per-IP rate limits, trusted-proxy list** | 401 vs 403 as the spec intends. Order and coupon endpoints are protected from abuse, and `X-Forwarded-For` can't be spoofed. |
| **Readiness, liveness, graceful shutdown** | A load balancer can stop routing traffic before in-flight requests are drained. |
| **Distroless, non-root, read-only containers with memory limits** | Small images with little to attack. A coupon build can't starve the host of memory. |

### Made only for this assignment

These shortcuts keep the project small and runnable with one command. Each sits behind an interface or a configuration setting, so it can be replaced without redesigning anything.

| Shortcut | In production |
|---|---|
| **Demo catalogue seeded by a migration**, with images hosted on the demo site | Products managed through an admin tool, with images on a CDN. |
| **Database credentials in `.env.defaults` and Compose** | `DATABASE_URL` from a secret manager, with TLS to the database. |
| **In-memory cache and rate-limit state**, per process | Valkey or Redis through the existing `cache.Cache` interface and a shared rate-limit store, so replicas share them. Not needed with one replica. |
| **Static API key** `apitest`, set in an environment variable | Keys issued, hashed and rotated through a secret manager, or real user authentication. |
| **One discount for every valid code**, in `coupon_settings` | Per-coupon rules: amount, expiry and conditions such as "lowest-priced item free". |
| **Coupon files on a local volume**, filled by an init container, checked every minute | Files in object storage, with the job triggered by an upload event. |
| **Full rebuild on any file change** | An incremental build that reuses the bucket files of unchanged files. |
| **Plain HTTP, CORS `*` by default** | TLS terminated at the load balancer, and CORS restricted to the shop's own domains. |

## API notes

| Endpoint | Notes |
|---|---|
| `GET /api/product` | All products |
| `GET /api/product/{productId}` | 400 if the ID isn't an integer, 404 if no product has it |
| `POST /api/order` | Needs an `api_key` with the `create_order` scope; rate limited |
| `POST /api/coupon/validate` | Not in the original spec. Returns `{couponCode, valid, discountPercent}`; rate limited |
| `GET /api/openapi.yaml` | The spec |
| `GET /health`, `GET /ready` | Liveness; readiness (503 during shutdown) |

Order status codes:
- **400:** malformed JSON, wrong types, a body over 64 KiB, or a Content-Type other than JSON.
- **401:** the key is missing or unknown.
- **403:** the key lacks the `create_order` scope.
- **422:** validation failed. Every problem is listed: empty items, a quantity outside 1–100, an unknown product or an invalid coupon.
- **429:** rate limited.
- **503:** the database or a coupon can't be checked right now.

Errors use the spec's `ApiResponse` shape: `{"code":422,"type":"validation_error","message":"..."}`.

Rate limits are token buckets per client IP: coupon checks get 20 per minute and orders 10 per minute. `X-Forwarded-For` is honoured only from `TRUSTED_PROXIES`.

The only spec changes are additions: `/coupon/validate`, plus 429 and 503 responses.

## Configuration

Every setting is an environment variable. Defaults for every program live in [.env.defaults](.env.defaults), and they work for local development. Each layer below overrides the ones before it, and override files only need the keys they change:

1. `.env.defaults`: committed, and baked into the images.
2. `.env`: local development overrides, gitignored. Compose also reads it.
3. The file named by `ENV_FILE`, e.g. `ENV_FILE=deploy/staging.env`, for staging or production.
4. Environment variables.

Shared by every program:

| Variable | Default | Notes |
|---|---|---|
| `DATABASE_URL` | `postgres://kart:kart@localhost:5432/kart?sslmode=disable` | Local development only; please set it from a secret elsewhere |
| `LOG_LEVEL`, `LOG_FORMAT` | `info`, `json` | |

kart-api:

| Variable | Default | Notes |
|---|---|---|
| `ADDR` | `:8080` | |
| `API_KEYS` | `apitest:create_order` | `key:scope\|scope`, comma-separated |
| `COUPONS_QUERY_TIMEOUT` | `2s` | Bounds each coupon lookup |
| `COUPON_CACHE_TTL`, `COUPON_NEGATIVE_CACHE_TTL`, `COUPON_SETTINGS_CACHE_TTL` | `5m`, `1m`, `1m` | `0` disables caching |
| `CACHE_MAX_ENTRIES` | `10000` | |
| `COUPON_RATE_LIMIT`/`_WINDOW`, `ORDER_RATE_LIMIT`/`_WINDOW` | `20`/`1m`, `10`/`1m` | |
| `TRUSTED_PROXIES` | none | CIDRs of load balancers |
| `CORS_ALLOWED_ORIGINS` | `*` | |
| `READ_HEADER_TIMEOUT`, `READ_TIMEOUT`, `WRITE_TIMEOUT`, `IDLE_TIMEOUT`, `SHUTDOWN_TIMEOUT` | `5s`, `10s`, `15s`, `60s`, `15s` | |

coupons-job:

| Variable | Default | Notes |
|---|---|---|
| `COUPONS_DIR` | `data/coupons` | Keep either the `.txt` or the `.gz` files there, never both, or every code counts as appearing in two files |
| `COUPONS_SCANNER` | `go` | `go` (the bucket scanner), `clickhouse` or `pebble`; switching rebuilds |
| `COUPONS_BUCKET_DIR` | `data/buckets` | About 8 bytes per coupon line; a fast disk helps. `/data/.buckets` in Compose |
| `COUPONS_PEBBLE_DIR` | `data/pebble` | Pebble scanner only: its database during a build, about 9 bytes per coupon line, deleted afterwards. `/data/.pebble` in Compose |
| `CLICKHOUSE_URL`, `COUPONS_CLICKHOUSE_DIR` | `clickhouse://kart:kart@localhost:9000/coupons`, `coupons` | ClickHouse scanner only: the server, and `COUPONS_DIR` as the server sees it, relative to its `user_files` folder |
| `COUPONS_MEMORY_LIMIT` | `256MiB` | At least `32MiB`; see [Memory budget](#memory-budget). The ClickHouse scanner's memory is capped on the server instead |
| `COUPONS_SCHEDULE` | `@every 1m` | A cron expression such as `*/5 * * * *`, or a descriptor |

The coupon rules and discount are not environment variables: they are in the [`coupon_settings`](#coupon-settings) table.

### Choosing a coupon scanner

The bucket scanner (`go`) is the default and needs nothing else. To compare the others with Docker Compose:

```sh
COUPONS_SCANNER=pebble docker compose up -d
COUPONS_SCANNER=clickhouse COMPOSE_PROFILES=clickhouse docker compose up -d   # also starts a ClickHouse server
```

The ClickHouse server reads the coupon files from the same volume, read-only. Switching scanners changes the build's rules, so the next run rebuilds and the logs show how long each took.

## Where things are

```
api/kart-api/                     API entry point: wiring, config, graceful shutdown, embedded OpenAPI spec
cmd/coupons-job/                  coupon build job: schedule, -once, -healthcheck
cmd/migrate/                      applies the database migrations
pkg/models/                       products, orders, coupons, settings, integer cents, shared errors
pkg/services/products/            product catalogue
pkg/services/orders/              order validation and pricing
pkg/services/coupons/             build job, memory budget, default pipeline
pkg/services/coupons/scanner/     bucket scanner: scatter, count, k-way merge, external sort
  clickhouse/                     ClickHouse scanner
  pebble/                         Pebble scanner
  scannertest/                    edge and stress cases every scanner must pass
pkg/services/coupons/codec/       packs codes into uint64
pkg/services/coupons/validator/   checks a code against the published set and settings
pkg/datasources/                  datasources.Open: the only place that picks implementations
pkg/datasources/*store/           store interfaces (products, orders, coupons) and the coupon cache decorators
pkg/datasources/couponsource/     coupon file reader interfaces
pkg/datasources/cache/            Cache interface and JSON helpers
pkg/datasources/internal/datastores/
  postgres/                       Postgres stores, advisory lock, embedded migrations
  memory/                         in-memory LRU/TTL cache
  files/                          folder reader with gzip support
pkg/handlers/kartapi/             Echo router, handlers, middleware, error rendering
pkg/helpers/                      layered config (Viper), slog setup, -healthcheck probe
.env.defaults                     default settings for every program
scripts/                          fetch-coupons.sh (fills the Docker volume)
data/coupons/                     coupon files for running without Docker (gitignored)
data/seed/                        optional local .gz copies for Docker (gitignored)
docs/animations/                  Manim animation of the bucket scanner, with its script
```

## Production scope

What a production deployment would add, roughly in order of need:

- **Incremental builds:** rebuild only the buckets of an added, removed or changed file (see [How the valid set is built](#how-the-valid-set-is-built)), so a change costs work in proportion to that file.
- **Alerts:** notify the team when coupons-job fails, falls behind its schedule, or publishes a very different number of codes than last time, and when Postgres is unreachable.
- **Error resistance in the build:** today any failing file, chunk or bucket fails the whole build, and the previous set stays published. Optionally, a failing piece could be skipped and reported instead. The worst case is then that a valid coupon is missing until the next build, never that an invalid one is accepted.
- **Blob storage:** keep the coupon files and the built bucket files in object storage, so a new job instance downloads only what changed instead of rebuilding from scratch, and a lost volume costs nothing.
- **Valkey or Redis, if needed:** not needed with one kart-api replica, because a Postgres primary-key lookup behind the memory cache is enough. With several replicas, it would give them shared rate limits and a shared negative cache, and pub/sub to broadcast `BustCache` when a build is published. The `cache.Cache` interface is where it would plug in.
- **Load balancing and replication:** no clear advantage yet. kart-api is stateless and light, and Postgres handles this load easily. Several kart-api replicas behind a load balancer, and a Postgres read replica for lookups, become worthwhile with real traffic or an availability target; both need only configuration, not code changes.
- **Coupon rules:** per-coupon discounts, expiry and conditions, and an admin endpoint for the settings.
- **Resilience:** serve an expired cached coupon answer while the database is erroring, and retry transient database errors.
- **Observability:** Prometheus metrics (latency, error rate, cache hit rate, build time and memory) and OpenTelemetry tracing.
- **Load tests:** measure throughput under realistic traffic to set the rate limits and pool sizes.
- **Delivery:** CI pushes images to a registry, with Kubernetes manifests or a Helm chart, running coupons-job as a CronJob with `-once`.
