// Command server runs the product cache API.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/omarrsherif/go-cache-api/internal/cache"
	"github.com/omarrsherif/go-cache-api/internal/config"
	"github.com/omarrsherif/go-cache-api/internal/handlers"
	"github.com/omarrsherif/go-cache-api/internal/metrics"
	"github.com/omarrsherif/go-cache-api/internal/repo"
	"github.com/omarrsherif/go-cache-api/internal/service"
)

const (
	maxBatchWorkers = 64
	maxBatchIDs     = 100
	dbConnectTries  = 10
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	log := newLogger(cfg.LogLevel)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := connectMySQL(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer db.Close()

	c := cache.New(cfg.RedisAddr, cfg.RedisOpTimeout, cfg.RedisPoolSize)
	defer c.Close()
	if err := c.Ping(ctx); err != nil {
		log.Warn("redis unreachable at startup; serving from MySQL until it returns", "addr", cfg.RedisAddr, "err", err)
	} else {
		log.Info("connected to redis", "addr", cfg.RedisAddr)
	}

	m := metrics.New()
	svc := service.New(repo.NewProductRepo(db), c, m, service.Options{
		TTL:            cfg.CacheTTL,
		StoreTimeout:   cfg.RequestTimeout,
		DefaultWorkers: cfg.BatchWorkers,
		MaxWorkers:     maxBatchWorkers,
		MaxBatchIDs:    maxBatchIDs,
	}, log)

	ready := &atomic.Bool{}
	ready.Store(true)

	mux := http.NewServeMux()
	handlers.NewProductHandler(svc, log).Register(mux)
	handlers.NewHealth(db, handlers.PingFunc(c.Ping), ready, m, map[string]any{
		"cache_ttl":         cfg.CacheTTL.String(),
		"redis_op_timeout":  cfg.RedisOpTimeout.String(),
		"redis_pool_size":   cfg.RedisPoolSize,
		"request_timeout":   cfg.RequestTimeout.String(),
		"batch_workers":     cfg.BatchWorkers,
		"max_batch_workers": maxBatchWorkers,
		"db_max_open_conns": cfg.DBMaxOpenConns,
		"db_max_idle_conns": cfg.DBMaxIdleConns,
	}).Register(mux)

	srv := &http.Server{
		Addr:              ":" + cfg.ServerPort,
		Handler:           handlers.Chain(mux, handlers.WithRecover(log), handlers.WithLogging(log), handlers.WithTimeout(cfg.RequestTimeout)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      cfg.RequestTimeout + 5*time.Second, // must exceed the request timeout so 504 bodies are delivered
		IdleTimeout:       120 * time.Second,
		// No BaseContext tied to the signal context: in-flight requests must
		// finish during Shutdown, not be cancelled the moment SIGTERM arrives.
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", srv.Addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("server: %w", err)
	case <-ctx.Done():
	}

	log.Info("shutdown signal received; draining", "timeout", cfg.ShutdownTimeout, "drain_delay", cfg.ShutdownDrainDelay)
	// Fail readiness first and give load balancers a chance to see it before
	// the listener closes. The delay is 0 unless SHUTDOWN_DRAIN_DELAY is set.
	ready.Store(false)
	time.Sleep(cfg.ShutdownDrainDelay)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	log.Info("shutdown complete")
	return nil
}

// connectMySQL retries for a while so the API can start alongside a MySQL
// container that is still initialising.
func connectMySQL(ctx context.Context, cfg config.Config, log *slog.Logger) (*sql.DB, error) {
	var lastErr error
	for attempt := 1; attempt <= dbConnectTries; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		db, err := repo.New(pingCtx, cfg)
		cancel()
		if err == nil {
			log.Info("connected to mysql", "host", cfg.DBHost, "port", cfg.DBPort, "max_open_conns", cfg.DBMaxOpenConns)
			return db, nil
		}
		lastErr = err
		log.Warn("mysql not ready", "attempt", attempt, "err", err)
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return nil, errors.New("interrupted while connecting to mysql")
		}
	}
	return nil, fmt.Errorf("mysql: giving up after %d attempts: %w", dbConnectTries, lastErr)
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
