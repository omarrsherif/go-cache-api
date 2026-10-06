# go-cache-api

A product catalogue API in Go that puts a Redis cache in front of MySQL using the
cache-aside pattern, and ships with a load tester that measures what the cache
actually buys you.

MySQL is the source of truth. Redis holds JSON copies of hot rows with a TTL.
Reads check Redis first; misses go to MySQL once (concurrent misses for the
same key are coalesced) and populate Redis. Writes update MySQL and delete the
cache key. If Redis is slow or down, every request still succeeds from MySQL.

## Highlights

- Full CRUD over HTTP plus a batch endpoint that fans out through a bounded worker pool
- Cache-aside with TTL, write-through invalidation, and request coalescing (`singleflight`)
- Redis failure is a cache miss, never an error: short per-operation timeouts and automatic fallback to MySQL
- Per-request timeouts that propagate to MySQL and Redis, graceful shutdown with a readiness flip
- Structured JSON errors, liveness and readiness endpoints, JSON metrics with cache hit rate
- Tests run under the race detector; a Go load tester produces reproducible benchmark reports

## Measured results

From `docs/benchmarks/latest.md` (24-core workstation, everything in Docker, MySQL limited
to 2 CPUs and Redis to 1; full method and caveats below):

| Metric | Result |
|---|---|
| Cache hit vs MySQL-only, p99 latency (50 clients) | **2.4 ms vs 55.4 ms, 96% lower** |
| Cache hit vs MySQL-only, throughput (50 clients) | **65k vs 24k req/s, 2.8x** |
| Backend time per read (Server-Timing), p50 / p99 | Redis 0.25 / 1.26 ms, MySQL 0.31 / 54.6 ms |
| Peak throughput, cache path, 500 concurrent clients | **115k req/s, p99 11 ms, zero errors** |
| Same load on MySQL only | 29k req/s, p99 85 ms |
| Cache hit rate, 90/10 read/write Zipf workload | **87.5%** |
| Cache stampede, 200 clients on one invalidated key | **98.4% of MySQL reads avoided** by coalescing |
| 50-id batch, 16-worker pool vs sequential | **2.5x faster** (p50 2.4 ms vs 5.9 ms) |


## Architecture

```
            +----------------------------------------------------------------+
  client -->|  handlers   JSON, error envelope, Cache-Control bypass, X-Cache |
            +----------------------------------------------------------------+
                     |
            +----------------------------------------------------------------+
            |  service    cache-aside | singleflight | worker pool | metrics  |
            +----------------------------------------------------------------+
               |                                         |
      +-----------------+                      +--------------------+
      |  cache (Redis)  |  miss / invalidate   |  repo (MySQL)      |
      |  JSON + TTL     | <------------------> |  source of truth   |
      +-----------------+                      +--------------------+
```

| Package | Responsibility |
|---|---|
| `cmd/server` | wiring, timeouts, signal handling, graceful shutdown |
| `cmd/loadtest` | benchmark scenarios, percentiles, Markdown/JSON report |
| `internal/handlers` | HTTP routes, JSON encoding, error mapping, middleware, health and metrics |
| `internal/service` | cache-aside reads, coalescing, batch reads, invalidation |
| `internal/cache` | Redis wrapper with per-op timeouts |
| `internal/repo` | MySQL queries and connection pool |
| `internal/workerpool` | generic bounded `Map` |
| `internal/metrics` | atomic counters and snapshots |
| `internal/config` | environment configuration with defaults |

## API

| Method | Path | Description |
|---|---|---|
| `POST` | `/products` | Create. Body `{"name","price","quantity"}`. Returns 201 and `Location`. |
| `GET` | `/products/{id}` | Read one. Response header `X-Cache: HIT`, `MISS` or `BYPASS`. |
| `PUT` | `/products/{id}` | Update and invalidate the cache entry. |
| `DELETE` | `/products/{id}` | Delete and invalidate. Returns 204. |
| `POST` | `/products/batch` | Read many. Body `{"ids":[...]}` (max 100). Query `mode=concurrent\|sequential`, `workers=N` (max 64). |
| `GET` | `/healthz` | Liveness. |
| `GET` | `/readyz` | Readiness: 503 while shutting down or when MySQL is unreachable. Redis state is reported but not required. |
| `GET` | `/metrics` | Counters (hits, misses, coalesced, db_reads, redis_errors, hit_rate), effective config, uptime. |

Prices are strings (`"19.99"`) so `DECIMAL(10,2)` round-trips exactly.

**Cache control.** A request may send `Cache-Control: no-cache` to skip the cache
lookup (and coalescing) and read MySQL directly, and `no-store` to skip
populating the cache. The load tester uses `no-cache, no-store` to measure the
MySQL-only path against the same server.

**Errors** are always `{"error":{"code":"...","message":"..."}}`:

| Status | Code |
|---|---|
| 400 | `validation_error`, `bad_request`, `batch_too_large` |
| 404 | `not_found` |
| 413 | `payload_too_large` |
| 504 | `timeout` (request exceeded `REQUEST_TIMEOUT`) |
| 500 | `internal_error` (details are logged, never returned) |

Examples:

```
curl -i -X POST localhost:8080/products -H "Content-Type: application/json" -d "{\"name\":\"Widget\",\"price\":\"19.99\",\"quantity\":3}"
curl -i localhost:8080/products/1                                   # X-Cache: MISS
curl -i localhost:8080/products/1                                   # X-Cache: HIT
curl -i -H "Cache-Control: no-cache" localhost:8080/products/1      # X-Cache: BYPASS
curl -i "localhost:8080/products/batch?workers=16" -H "Content-Type: application/json" -d "{\"ids\":[1,2,3,999]}"
curl localhost:8080/metrics
```

## Running it

Requirements: Go 1.27+, Docker with Compose.

```
cp .env.example .env            # passwords for MySQL; everything else has defaults
docker compose up -d mysql redis
go run ./cmd/server             # http://localhost:8080
```

Or everything in Docker:

```
docker compose up -d --build    # API on :8080; set API_PORT=8090 if 8080 is taken
```

`schema.sql` is applied by MySQL on first start of the data volume. Run
`docker compose down -v` to reset the data.

If you previously started containers by hand as `cache-api-mysql` and
`cache-api-redis`, remove them first so the ports are free:
`docker rm -f cache-api-mysql cache-api-redis`.

### Configuration

All settings come from the environment (or `.env`). Only `DB_*` are required.

| Variable | Default | Meaning |
|---|---|---|
| `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME` | required | MySQL connection |
| `REDIS_ADDR` | `127.0.0.1:6380` | Redis address |
| `SERVER_PORT` | `8080` | HTTP listen port |
| `LOG_LEVEL` | `info` | `debug` logs one line per request |
| `CACHE_TTL` | `60s` | lifetime of a cached product |
| `REDIS_OP_TIMEOUT` | `100ms` | per Redis command; on expiry the request continues with MySQL |
| `REDIS_POOL_SIZE` | `100` | Redis connection pool |
| `REQUEST_TIMEOUT` | `5s` | per-request deadline, propagated to MySQL and Redis |
| `SHUTDOWN_TIMEOUT` | `10s` | drain time on SIGINT/SIGTERM |
| `SHUTDOWN_DRAIN_DELAY` | `0s` | pause between failing `/readyz` and closing the listener, so a load balancer can stop routing first |
| `BATCH_WORKERS` | `8` | default worker-pool size for batch reads (max 64) |
| `DB_MAX_OPEN_CONNS`, `DB_MAX_IDLE_CONNS`, `DB_CONN_MAX_LIFETIME` | `25`, `25`, `5m` | MySQL pool |

## Tests

```
go test -race ./...                 # or scripts\test.ps1 / make test-race
```

Unit tests use an in-memory store and [miniredis](https://github.com/alicebob/miniredis),
so they need no services. They cover cache-aside behaviour, invalidation,
Redis-down fallback, request coalescing (100 concurrent misses, one MySQL
read), the detached-context guarantee that a cancelled caller does not fail
the others waiting on the same key, worker-pool bounds and cancellation, and
every HTTP status path. The repository tests run against the real MySQL from
`.env` and skip when it is not reachable.

Micro-benchmarks for the service and worker pool (in-process Redis, fake
store with simulated latency):

```
go test -bench . -benchmem -run '^$' ./internal/service/ ./internal/workerpool/
```

| Benchmark | Result |
|---|---|
| `Get` cache hit (service overhead, in-process Redis) | 4.0 µs/op, 23 allocs |
| `GetMany` 50 ids, sequential, 1 ms simulated store | 77.7 ms/op |
| `GetMany` 50 ids, 8 / 16 / 32 workers | 10.9 / 6.2 / 3.1 ms/op |

## Benchmarks

```
scripts\bench.ps1            # Windows; add -Quick for a shorter run, -Port 8090 if 8080 is busy
make bench                   # Linux / macOS
```

The script starts MySQL and Redis in Docker, runs the API natively from
`bin/server.exe` with `CACHE_TTL=10m` and `LOG_LEVEL=warn`, seeds 1000
products, runs the scenarios below with a closed-loop load generator, and
writes `docs/benchmarks/latest.md` and `latest.json`.

| Scenario | What it measures |
|---|---|
| `read_cache_hit` | single-key GETs with a warm cache: Redis-hit latency and throughput |
| `read_mysql_only` | the same GETs with `Cache-Control: no-cache, no-store`: MySQL-only latency |
| `mixed_zipf` | 90% reads / 10% writes over a Zipf key distribution: realistic hit rate |
| `batch_*` | 50-id batches, sequential vs worker pools of 1/4/8/16/32 |
| `ramp_*` | cache vs MySQL-only at 50, 200 and 500 concurrent clients |
| `stampede_coalescing` | 200 clients on one key invalidated every 100 ms: MySQL reads avoided by coalescing |

### Results

Full report: [docs/benchmarks/latest.md](docs/benchmarks/latest.md) (JSON alongside it).
Environment: Windows 11 host, Docker Desktop with 24 CPUs, all four components as
containers on one Compose network, 1000 products, Go 1.27. MySQL is limited to
2 CPUs and Redis to 1 by `docker-compose.bench.yml`; see "Why MySQL is CPU-limited" below.

**Single-key reads, 50 concurrent clients.** Backend columns come from the API's
`Server-Timing` header and cover only the Redis or MySQL call.

| Scenario | RPS | p50 ms | p95 ms | p99 ms | Backend p50 ms | Backend p99 ms |
|---|---|---|---|---|---|---|
| read_cache_hit | 65067 | 0.67 | 1.63 | 2.38 | 0.246 | 1.255 |
| read_mysql_only | 23581 | 0.82 | 2.60 | 55.39 | 0.311 | 54.618 |

**Heavy concurrent traffic.**

| Path | Clients | RPS | p50 ms | p99 ms | Errors |
|---|---|---|---|---|---|
| cache | 50 | 67074 | 0.65 | 2.22 | 0 |
| cache | 200 | 113124 | 1.51 | 5.59 | 0 |
| cache | 500 | 114628 | 4.00 | 11.44 | 0 |
| MySQL only | 50 | 24362 | 0.78 | 57.33 | 0 |
| MySQL only | 200 | 28059 | 2.15 | 73.21 | 0 |
| MySQL only | 500 | 29070 | 5.27 | 85.44 | 0 |

**Mixed workload** (90% GET / 10% PUT, Zipf s=1.1): 13.7k req/s, GET p50 0.29 ms,
hit rate 87.5% from server counters (161k hits, 23k misses, 19.6k MySQL reads).

**Batch of 50 ids, 4 concurrent clients, MySQL-only path.**

| Mode | Workers | Batches/s | p50 ms | Speedup (p50) |
|---|---|---|---|---|
| sequential | 1 | 634 | 5.94 | 1.0x |
| concurrent | 4 | 545 | 3.64 | 1.6x |
| concurrent | 8 | 597 | 2.79 | 2.1x |
| concurrent | 16 | 636 | 2.41 | 2.5x |
| concurrent | 32 | 654 | 2.40 | 2.5x |

The pool stops helping around 16 workers because 4 clients x 16 workers already
keeps the 2-CPU MySQL saturated. On the cached path the same matrix reaches
2.5x with 124k items/s at 32 workers.

**Cache stampede.** 200 clients hammer one key while a writer invalidates it every
100 ms: 1.14M requests, 12992 cache misses, 214 MySQL reads, 12778 coalesced, so
98.4% of the misses never reached MySQL.

#### Why MySQL is CPU-limited

[docs/benchmarks/unconstrained-mysql.md](docs/benchmarks/unconstrained-mysql.md)
is the same suite with MySQL on all 24 cores. There, a primary-key lookup from the
InnoDB buffer pool costs the same 0.7 ms end to end as a Redis hit at 50 clients,
and the cache only pulls ahead at 500 clients (110k vs 93k req/s, p99 10 ms vs
18 ms). That is a real result: for a tiny hot table with a generous connection
pool, MySQL is a cache. The 2-CPU limit models the usual production shape, a
shared database that cannot scale with the API tier, and is where the p99 tail
(CFS throttling once the quota is spent) comes from. Both reports are committed;
quote whichever matches your deployment.


## Design notes

- **Cache-aside, write-around.** Reads populate the cache; creates do not. Updates and deletes write MySQL first, then delete the key. A reader that fetched the old row just before the write could re-populate the stale value, which then lives at most `CACHE_TTL`. This window is accepted rather than closed with delayed double-deletes.
- **Coalescing with a detached context.** `singleflight` runs one fetch per key. The shared fetch uses `context.WithoutCancel` plus its own timeout, so a caller that disconnects does not fail the others, and each caller still stops waiting when its own deadline passes. `no-cache` reads bypass coalescing so MySQL-only measurements are honest per-request round trips.
- **Bounded worker pool.** `workerpool.Map` runs N goroutines over an unbuffered job channel and writes results by index. One worker runs inline, which keeps the sequential baseline free of goroutine overhead.
- **Redis is optional at runtime.** Every Redis call has `REDIS_OP_TIMEOUT`; failures increment `redis_errors` and the read proceeds to MySQL. Readiness does not depend on Redis.
- **Timeouts and shutdown.** A middleware applies `REQUEST_TIMEOUT` to the request context. On SIGINT/SIGTERM the server flips `/readyz` to 503, waits `SHUTDOWN_DRAIN_DELAY`, stops accepting connections, and waits up to `SHUTDOWN_TIMEOUT` for in-flight requests, which keep their own contexts and are not cancelled by the signal.
- **Not negatively cached.** A read for a missing id always reaches MySQL (coalesced, but not stored).

## Layout

```
cmd/server/            API entry point
cmd/loadtest/          benchmark tool
internal/cache/        Redis wrapper
internal/config/       settings
internal/handlers/     HTTP layer
internal/metrics/      counters
internal/models/       Product type
internal/repo/         MySQL
internal/service/      cache-aside + batch logic
internal/workerpool/   bounded Map
docs/benchmarks/       generated reports
scripts/               PowerShell helpers (up, test, seed, bench)
docker-compose.yml     mysql, redis, api
Dockerfile             multi-stage build
Makefile               POSIX equivalents of the scripts
```
