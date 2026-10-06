# go-cache-api benchmark results

Generated 2026-10-05 23:27:53

## Environment

| Item | Value |
|---|---|
| OS / arch | linux/amd64 |
| CPUs | 24 |
| Go | go1.27.1 |
| Topology | all services in Docker (compose network); load generator, API, Redis and MySQL as containers |
| Products seeded | 1000 |
| Timer resolution | 29ns |
| Resource limits | mysql cpus=2, redis cpus=1, api and loadtest unlimited (24 CPUs shared) |
| server batch_workers | 8 |
| server cache_ttl | 10m0s |
| server db_max_idle_conns | 64 |
| server db_max_open_conns | 64 |
| server max_batch_workers | 64 |
| server redis_op_timeout | 100ms |
| server redis_pool_size | 100 |
| server request_timeout | 5s |

Latencies are client-observed, measured over a scenario window after a 2s warm-up. "Little mean" is concurrency / RPS, a timer-independent mean latency.

## 1. Single-key reads: Redis cache hit vs MySQL-only

End-to-end columns are client-observed. Backend columns come from the API's `Server-Timing` header and cover only the service call (Redis or MySQL), excluding HTTP handling.

| Scenario | Conc | Requests | RPS | p50 ms | p95 ms | p99 ms | max ms | Backend p50 ms | Backend p99 ms | Errors | X-Cache |
|---|---|---|---|---|---|---|---|---|---|---|---|
| read_cache_hit | 50 | 975999 | 65067 | 0.67 | 1.63 | 2.38 | 5.66 | 0.246 | 1.255 | 0 | HIT 100% |
| read_mysql_only | 50 | 353716 | 23581 | 0.82 | 2.60 | 55.39 | 66.96 | 0.311 | 54.618 | 0 | BYPASS 100% |

**Cache hit vs MySQL-only, end to end:** p50 0.67 ms vs 0.82 ms (18% lower); p99 2.38 ms vs 55.39 ms (96% lower); throughput 65067 vs 23581 req/s (2.8x).

**Backend only (Redis GET vs MySQL query):** p50 0.246 ms vs 0.311 ms (1.3x faster, 21% lower); p99 1.255 ms vs 54.618 ms (43.5x faster).

## 2. Mixed workload: 90% reads / 10% writes, Zipf(1.1) key distribution

| Conc | Requests | RPS | GET p50/p99 ms | PUT p50/p99 ms | cache_hits | cache_misses | db_reads | Hit rate |
|---|---|---|---|---|---|---|---|---|
| 50 | 205172 | 13678 | 0.29 / 0.91 | 4.98 / 171.85 | 161512 | 22988 | 19644 | 87.5% |

Client-observed X-Cache: HIT 88%, MISS 12%. Every PUT invalidates its key, so the next read of that key misses.

## 3. Batch of 50 ids: sequential vs bounded worker pool (MySQL-only path)

| Mode | Workers | Conc | Requests | Batches/s | Items/s | p50 ms | p95 ms | p99 ms | Speedup vs sequential (p50) |
|---|---|---|---|---|---|---|---|---|---|
| sequential | 1 | 4 | 6341 | 634.1 | 31705 | 5.94 | 8.66 | 11.41 | 1.0x |
| concurrent | 1 | 4 | 7242 | 724.2 | 36210 | 5.35 | 6.68 | 8.53 | 1.1x |
| concurrent | 4 | 4 | 5449 | 544.9 | 27245 | 3.64 | 50.07 | 60.53 | 1.6x |
| concurrent | 8 | 4 | 5968 | 596.8 | 29840 | 2.79 | 56.94 | 64.20 | 2.1x |
| concurrent | 16 | 4 | 6355 | 635.5 | 31775 | 2.41 | 56.90 | 65.80 | 2.5x |
| concurrent | 32 | 4 | 6537 | 653.7 | 32685 | 2.40 | 55.56 | 65.37 | 2.5x |

## 3b. Batch of 50 ids on the cached path

| Mode | Workers | Conc | Requests | Batches/s | Items/s | p50 ms | p95 ms | p99 ms | Speedup vs sequential (p50) |
|---|---|---|---|---|---|---|---|---|---|
| sequential | 1 | 4 | 10128 | 1012.8 | 50640 | 3.88 | 4.66 | 5.37 | 1.0x |
| concurrent | 1 | 4 | 9962 | 996.2 | 49810 | 3.94 | 4.68 | 5.34 | 1.0x |
| concurrent | 4 | 4 | 16296 | 1629.6 | 81480 | 2.39 | 3.32 | 3.83 | 1.6x |
| concurrent | 8 | 4 | 20164 | 2016.4 | 100820 | 1.89 | 2.94 | 3.51 | 2.1x |
| concurrent | 16 | 4 | 22618 | 2261.8 | 113090 | 1.65 | 2.79 | 3.40 | 2.3x |
| concurrent | 32 | 4 | 24856 | 2485.6 | 124280 | 1.53 | 2.55 | 3.15 | 2.5x |

## 4. Heavy concurrent traffic

| Path | Conc | Requests | RPS | p50 ms | p95 ms | p99 ms | Backend p50 ms | Backend p99 ms | Errors | 504s |
|---|---|---|---|---|---|---|---|---|---|---|
| ramp_cache | 50 | 670743 | 67074 | 0.65 | 1.55 | 2.22 | 0.247 | 1.163 | 0 | 0 |
| ramp_cache | 200 | 1131235 | 113124 | 1.51 | 3.99 | 5.59 | 0.948 | 4.187 | 0 | 0 |
| ramp_cache | 500 | 1146280 | 114628 | 4.00 | 7.80 | 11.44 | 3.195 | 6.857 | 0 | 0 |
| ramp_mysql_only | 50 | 243620 | 24362 | 0.78 | 2.19 | 57.33 | 0.316 | 56.637 | 0 | 0 |
| ramp_mysql_only | 200 | 280588 | 28059 | 2.15 | 66.25 | 73.21 | 1.217 | 71.115 | 0 | 0 |
| ramp_mysql_only | 500 | 290695 | 29070 | 5.27 | 77.40 | 85.44 | 3.674 | 82.711 | 0 | 0 |

## 5. Cache stampede and request coalescing

200 clients read one hot key while a writer invalidates it every 100 ms.

| Requests | RPS | p50 ms | p99 ms | cache_misses | db_reads | coalesced | MySQL reads avoided |
|---|---|---|---|---|---|---|---|
| 1140351 | 114035 | 1.50 | 5.87 | 12992 | 214 | 12778 | 98.4% |

## Notes

- Closed-loop load generator: each client sends its next request as soon as the previous one returns.
- The load generator, API, Redis and MySQL all share one machine's CPU, so absolute numbers depend on the hardware; the ratios between scenarios are the point.
- Products are a single table read by primary key, which is MySQL's best case; the cache advantage grows with query cost and network distance to the database.
- Cache misses are not negatively cached; a missing id always reaches MySQL.
- Reproduce with `scripts\bench.ps1` (Windows) or `make bench`.
