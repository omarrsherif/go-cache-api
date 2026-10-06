# go-cache-api benchmark results (unconstrained MySQL: 24 CPUs)

Generated 2026-10-05 23:19:15

## Environment

| Item | Value |
|---|---|
| OS / arch | linux/amd64 |
| CPUs | 24 |
| Go | go1.27.1 |
| Topology | all services in Docker (compose network); load generator, API, Redis and MySQL as containers |
| Products seeded | 1000 |
| Timer resolution | 29ns |
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

| Scenario | Conc | Requests | RPS | p50 ms | p95 ms | p99 ms | max ms | Little mean ms | Errors | X-Cache |
|---|---|---|---|---|---|---|---|---|---|---|
| read_cache_hit | 50 | 958637 | 63909 | 0.68 | 1.68 | 2.42 | 5.58 | 0.78 | 0 | HIT 100% |
| read_mysql_only | 50 | 900763 | 60051 | 0.70 | 1.87 | 2.70 | 6.59 | 0.83 | 0 | BYPASS 100% |

**Cache hit vs MySQL-only:** p50 latency 0.68 ms vs 0.70 ms (1.0x lower, 3% reduction); p99 2.42 ms vs 2.70 ms (1.1x lower); throughput 63909 vs 60051 req/s (1.1x higher).

## 2. Mixed workload: 90% reads / 10% writes, Zipf(1.1) key distribution

| Conc | Requests | RPS | GET p50/p99 ms | PUT p50/p99 ms | cache_hits | cache_misses | db_reads | Hit rate |
|---|---|---|---|---|---|---|---|---|
| 50 | 66096 | 4406 | 0.23 / 0.67 | 15.56 / 592.73 | 53033 | 6561 | 6085 | 89.0% |

Client-observed X-Cache: HIT 89%, MISS 11%. Every PUT invalidates its key, so the next read of that key misses.

## 3. Batch of 50 ids: sequential vs bounded worker pool (MySQL-only path)

| Mode | Workers | Conc | Requests | Batches/s | Items/s | p50 ms | p95 ms | p99 ms | Speedup vs sequential (p50) |
|---|---|---|---|---|---|---|---|---|---|
| sequential | 1 | 4 | 7341 | 734.1 | 36705 | 5.37 | 6.29 | 6.91 | 1.0x |
| concurrent | 1 | 4 | 7113 | 711.3 | 35565 | 5.50 | 6.72 | 7.74 | 1.0x |
| concurrent | 4 | 4 | 11920 | 1192.0 | 59600 | 3.27 | 4.20 | 4.74 | 1.6x |
| concurrent | 8 | 4 | 16732 | 1673.2 | 83660 | 2.24 | 3.47 | 4.06 | 2.4x |
| concurrent | 16 | 4 | 20448 | 2044.8 | 102240 | 1.77 | 3.21 | 3.81 | 3.0x |
| concurrent | 32 | 4 | 18749 | 1874.9 | 93745 | 1.93 | 3.52 | 4.23 | 2.8x |

## 3b. Batch of 50 ids on the cached path

| Mode | Workers | Conc | Requests | Batches/s | Items/s | p50 ms | p95 ms | p99 ms | Speedup vs sequential (p50) |
|---|---|---|---|---|---|---|---|---|---|
| sequential | 1 | 4 | 8689 | 868.9 | 43445 | 4.45 | 5.86 | 7.10 | 1.0x |
| concurrent | 1 | 4 | 9909 | 990.9 | 49545 | 3.97 | 4.74 | 5.22 | 1.1x |
| concurrent | 4 | 4 | 16054 | 1605.4 | 80270 | 2.42 | 3.36 | 3.80 | 1.8x |
| concurrent | 8 | 4 | 19603 | 1960.3 | 98015 | 1.95 | 2.99 | 3.60 | 2.3x |
| concurrent | 16 | 4 | 22705 | 2270.5 | 113525 | 1.65 | 2.81 | 3.45 | 2.7x |
| concurrent | 32 | 4 | 24040 | 2404.0 | 120200 | 1.58 | 2.62 | 3.33 | 2.8x |

## 4. Heavy concurrent traffic

| Path | Conc | Requests | RPS | p50 ms | p95 ms | p99 ms | Little mean ms | Errors | 504s |
|---|---|---|---|---|---|---|---|---|---|
| ramp_cache | 50 | 625597 | 62560 | 0.69 | 1.70 | 2.47 | 0.80 | 0 | 0 |
| ramp_cache | 200 | 1010053 | 101005 | 1.70 | 4.40 | 6.21 | 1.98 | 0 | 0 |
| ramp_cache | 500 | 1103743 | 110374 | 4.20 | 7.60 | 10.28 | 4.53 | 0 | 0 |
| ramp_mysql_only | 50 | 650519 | 65052 | 0.68 | 1.54 | 2.30 | 0.77 | 0 | 0 |
| ramp_mysql_only | 200 | 915979 | 91598 | 1.79 | 5.27 | 7.28 | 2.18 | 0 | 0 |
| ramp_mysql_only | 500 | 928335 | 92834 | 4.44 | 13.14 | 18.38 | 5.39 | 0 | 0 |

## 5. Cache stampede and request coalescing

200 clients read one hot key while a writer invalidates it every 100 ms.

| Requests | RPS | p50 ms | p99 ms | cache_misses | db_reads | coalesced | MySQL reads avoided |
|---|---|---|---|---|---|---|---|
| 1114785 | 111478 | 1.52 | 5.81 | 12647 | 204 | 12443 | 98.4% |

## Notes

- Closed-loop load generator: each client sends its next request as soon as the previous one returns.
- The load generator, API, Redis and MySQL all share one machine's CPU, so absolute numbers depend on the hardware; the ratios between scenarios are the point.
- Products are a single table read by primary key, which is MySQL's best case; the cache advantage grows with query cost and network distance to the database.
- Cache misses are not negatively cached; a missing id always reaches MySQL.
- Reproduce with `scripts\bench.ps1` (Windows) or `make bench`.
