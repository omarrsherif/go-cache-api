// Package config loads application settings from environment variables, with
// an optional .env file for local development.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

// Config holds every runtime setting. Only the DB_* variables are required;
// everything else has a sensible default.
type Config struct {
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string

	RedisAddr  string
	ServerPort string
	LogLevel   string

	CacheTTL       time.Duration
	RedisOpTimeout time.Duration
	RedisPoolSize  int

	RequestTimeout     time.Duration
	ShutdownTimeout    time.Duration
	ShutdownDrainDelay time.Duration // time between failing /readyz and closing the listener
	BatchWorkers       int

	DBMaxOpenConns    int
	DBMaxIdleConns    int
	DBConnMaxLifetime time.Duration
}

// Load reads settings from the environment (and .env if present) and
// validates them. The .env file never overrides variables already set.
func Load() (Config, error) {
	_ = godotenv.Load()

	cfg := Config{
		DBHost:     os.Getenv("DB_HOST"),
		DBPort:     os.Getenv("DB_PORT"),
		DBUser:     os.Getenv("DB_USER"),
		DBPassword: os.Getenv("DB_PASSWORD"),
		DBName:     os.Getenv("DB_NAME"),
		RedisAddr:  envString("REDIS_ADDR", "127.0.0.1:6380"),
		ServerPort: envString("SERVER_PORT", "8080"),
		LogLevel:   envString("LOG_LEVEL", "info"),
	}

	for _, kv := range [][2]string{
		{"DB_HOST", cfg.DBHost}, {"DB_PORT", cfg.DBPort}, {"DB_USER", cfg.DBUser},
		{"DB_PASSWORD", cfg.DBPassword}, {"DB_NAME", cfg.DBName},
	} {
		if kv[1] == "" {
			return Config{}, fmt.Errorf("missing environment variable: %s", kv[0])
		}
	}

	var err error
	if cfg.CacheTTL, err = envDuration("CACHE_TTL", 60*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.RedisOpTimeout, err = envDuration("REDIS_OP_TIMEOUT", 100*time.Millisecond); err != nil {
		return Config{}, err
	}
	if cfg.RedisPoolSize, err = envInt("REDIS_POOL_SIZE", 100); err != nil {
		return Config{}, err
	}
	if cfg.RequestTimeout, err = envDuration("REQUEST_TIMEOUT", 5*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = envDuration("SHUTDOWN_TIMEOUT", 10*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownDrainDelay, err = envDuration("SHUTDOWN_DRAIN_DELAY", 0); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownDrainDelay < 0 {
		return Config{}, fmt.Errorf("SHUTDOWN_DRAIN_DELAY must not be negative")
	}
	if cfg.BatchWorkers, err = envInt("BATCH_WORKERS", 8); err != nil {
		return Config{}, err
	}
	if cfg.DBMaxOpenConns, err = envInt("DB_MAX_OPEN_CONNS", 25); err != nil {
		return Config{}, err
	}
	if cfg.DBMaxIdleConns, err = envInt("DB_MAX_IDLE_CONNS", 25); err != nil {
		return Config{}, err
	}
	if cfg.DBConnMaxLifetime, err = envDuration("DB_CONN_MAX_LIFETIME", 5*time.Minute); err != nil {
		return Config{}, err
	}

	if cfg.DBMaxIdleConns > cfg.DBMaxOpenConns {
		return Config{}, fmt.Errorf("DB_MAX_IDLE_CONNS (%d) must not exceed DB_MAX_OPEN_CONNS (%d)", cfg.DBMaxIdleConns, cfg.DBMaxOpenConns)
	}
	if cfg.BatchWorkers < 1 || cfg.RedisPoolSize < 1 || cfg.DBMaxOpenConns < 1 {
		return Config{}, fmt.Errorf("BATCH_WORKERS, REDIS_POOL_SIZE and DB_MAX_OPEN_CONNS must be at least 1")
	}
	for name, d := range map[string]time.Duration{
		"CACHE_TTL": cfg.CacheTTL, "REDIS_OP_TIMEOUT": cfg.RedisOpTimeout,
		"REQUEST_TIMEOUT": cfg.RequestTimeout, "SHUTDOWN_TIMEOUT": cfg.ShutdownTimeout,
		"DB_CONN_MAX_LIFETIME": cfg.DBConnMaxLifetime,
	} {
		if d <= 0 {
			return Config{}, fmt.Errorf("%s must be a positive duration", name)
		}
	}
	return cfg, nil
}

func envString(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envInt(name string, def int) (int, error) {
	v := os.Getenv(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not an integer", name, v)
	}
	return n, nil
}

func envDuration(name string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(name)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a duration (e.g. 500ms, 10s, 5m)", name, v)
	}
	return d, nil
}
