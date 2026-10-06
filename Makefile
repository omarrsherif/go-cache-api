# On Windows use the PowerShell scripts in scripts\ (or mingw32-make from MSYS2).
PORT ?= 8080
BENCH_ARGS ?=

.PHONY: tidy fmt vet build test test-race bench-micro up up-all down down-v run seed bench bench-quick bench-native

tidy:
	go mod tidy

fmt:
	gofmt -l -w .

vet:
	go vet ./...

build:
	go build -o bin/server ./cmd/server
	go build -o bin/loadtest ./cmd/loadtest

test:
	go test ./...

test-race:
	go test -race ./...
	go test -race -count=5 ./internal/service/ ./internal/workerpool/

bench-micro:
	go test -bench . -benchmem -run '^$$' ./internal/service/ ./internal/workerpool/

up:
	docker compose up -d mysql redis

up-all:
	docker compose up -d --build

down:
	docker compose down

down-v:
	docker compose down -v

run:
	go run ./cmd/server

seed:
	go run ./cmd/loadtest -seed-only -products 1000 -url http://127.0.0.1:$(PORT)

# Full HTTP benchmark with every component in Docker; report in docs/benchmarks/latest.md
bench:
	mkdir -p docs/benchmarks
	API_PORT=$(PORT) docker compose -f docker-compose.yml -f docker-compose.bench.yml run --rm --build loadtest -products 1000 $(BENCH_ARGS)
	docker compose -f docker-compose.yml -f docker-compose.bench.yml stop api

bench-quick:
	$(MAKE) bench BENCH_ARGS="-quick $(BENCH_ARGS)"

# Same suite with the API and load generator on the host (MySQL/Redis in Docker)
bench-native: up build
	SERVER_PORT=$(PORT) CACHE_TTL=10m LOG_LEVEL=warn DB_MAX_OPEN_CONNS=64 DB_MAX_IDLE_CONNS=64 ./bin/server & echo $$! > bin/server.pid; \
	for i in $$(seq 1 60); do curl -sf http://127.0.0.1:$(PORT)/readyz >/dev/null && break; sleep 1; done; \
	./bin/loadtest -url http://127.0.0.1:$(PORT) -products 1000 $(BENCH_ARGS); code=$$?; \
	kill -INT $$(cat bin/server.pid); rm -f bin/server.pid; exit $$code
