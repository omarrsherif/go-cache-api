// Package repo is the only package that talks to MySQL.
package repo

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/go-sql-driver/mysql"

	"github.com/omarrsherif/go-cache-api/internal/config"
)

// New opens a MySQL connection pool, applies the pool limits from cfg, and
// verifies connectivity with a ping bounded by ctx.
//
// clientFoundRows makes UPDATE report matched rows rather than changed rows,
// so saving a product without modifying it is not mistaken for a missing row.
// interpolateParams sends each query in one round trip instead of
// prepare/execute/close; the driver still escapes every parameter.
func New(ctx context.Context, cfg config.Config) (*sql.DB, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&clientFoundRows=true&charset=utf8mb4&interpolateParams=true",
		cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort, cfg.DBName)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	db.SetMaxOpenConns(cfg.DBMaxOpenConns)
	db.SetMaxIdleConns(cfg.DBMaxIdleConns)
	db.SetConnMaxLifetime(cfg.DBConnMaxLifetime)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}
	return db, nil
}
