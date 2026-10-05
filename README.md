# go-cache-api

A product catalog service in Go. MySQL is the source of truth; a Redis cache and
an HTTP API are planned on top of it.

This is a work in progress that is being built one layer at a time. The data
layer is finished and tested. There is no HTTP server yet.

## Status

| Layer | State |
| --- | --- |
| Settings loaded from the environment (`internal/config`) | Done |
| MySQL connection pool (`internal/repo/mysql.go`) | Done |
| Product repository: create, get, update, delete (`internal/repo/product.go`) | Done, with integration tests |
| HTTP handlers and server (`internal/handlers`) | Not started |
| Redis cache in front of MySQL | Not started |

`go run ./cmd/server` currently loads the settings, connects to MySQL, logs
`connected to MySQL`, and exits.

## Layout

```
cmd/server/main.go          entry point
internal/config/            reads settings from the environment or .env
internal/models/product.go  the Product struct
internal/repo/mysql.go      opens and checks the MySQL connection pool
internal/repo/product.go    product CRUD, the only place that runs SQL
internal/handlers/          HTTP handlers (empty for now)
schema.sql                  creates the products table
.env.example                the settings the app expects
```

## Running it

You need Go 1.27.1 or newer and Docker.

1. Start MySQL and Redis. The MySQL command mounts `schema.sql` so the
   `products` table is created the first time the container starts. Run it from
   the repository root (PowerShell, macOS, or Linux):

   ```
   docker run -d --name cache-api-mysql -p 127.0.0.1:3307:3306 -e MYSQL_ROOT_PASSWORD=change-me-too -e MYSQL_DATABASE=cache_api -e MYSQL_USER=app -e MYSQL_PASSWORD=change-me -v "${PWD}/schema.sql:/docker-entrypoint-initdb.d/schema.sql:ro" mysql:8.4
   docker run -d --name cache-api-redis -p 127.0.0.1:6380:6379 redis:8
   ```

   Both containers listen on `127.0.0.1` only, on ports 3307 and 6380, so they
   do not clash with a MySQL or Redis already installed on the machine.

2. Create your settings file. The values in `.env.example` match the commands
   above; change the passwords in both places if you want different ones.

   ```
   cp .env.example .env
   ```

3. Run the program:

   ```
   go run ./cmd/server
   ```

Every setting in `.env.example` is required, including `REDIS_ADDR` and
`SERVER_PORT`, even though nothing uses those two yet. The program stops at
startup and names the missing variable if one is not set.

## Tests

```
go test ./...
```

The repository tests are integration tests. They run against the real MySQL
from step 1 rather than a mock, and they delete the rows they create. If MySQL
is not reachable they are skipped, not failed, so check the output for `SKIP`
before trusting a green run.

## Design notes

- **Money is never a float.** `price` is `DECIMAL(10,2)` in MySQL and a string
  in Go, so a value like 19.99 is stored and returned exactly.
- **Every query is parameterised.** Values are passed separately from the SQL
  text with `?` placeholders, which rules out SQL injection.
- **"Not found" is a real error value.** The repository returns `ErrNotFound`
  for a missing product, so a caller can tell it apart from a database failure
  with `errors.Is`.
- **Updates that change nothing still count as found.** The connection uses
  `clientFoundRows=true`, so saving a product without editing it is not mistaken
  for a missing row.
- **Credentials stay out of the code.** They come from the environment or a
  local `.env` file that git ignores.

The source is commented in much more detail than usual. Each file explains what
the code does and why it is written that way.
