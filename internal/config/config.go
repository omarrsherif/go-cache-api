// Package config reads our settings (database address, password, and so on)
// out of the environment, so that none of those values are typed directly
// into the source code. That matters because the code gets committed to git
// but the password must not be.
package config

import (
	"fmt"
	"os" // "os" lets us read environment variables.

	// godotenv is a small helper that reads a ".env" file and loads whatever
	// is inside it into the environment, as if we had typed those values
	// into the terminal ourselves.
	"github.com/joho/godotenv"
)

// Config is a container holding every setting the program needs.
// Grouping them in one struct means we pass around a single tidy value
// instead of seven loose strings.
type Config struct {
	DBHost     string // where MySQL is running, e.g. "127.0.0.1"
	DBPort     string // which port MySQL listens on, e.g. "3307"
	DBUser     string // the MySQL username
	DBPassword string // the MySQL password
	DBName     string // which database inside MySQL to use
	RedisAddr  string // where Redis is running (not used yet)
	ServerPort string // which port our own web server will listen on
}

// Load gathers all the settings and checks that none are missing.
// It returns the filled-in Config, or an error naming the first setting
// that was not provided.
func Load() (Config, error) {
	// Try to read the .env file sitting next to the program.
	//
	// The "_ =" in front means "ignore the error". That is deliberate: when
	// this app runs inside Docker the settings are handed to it directly and
	// there is no .env file at all. A missing .env is normal, not a failure.
	_ = godotenv.Load()

	// os.Getenv fetches one environment variable by name. If the variable was
	// never set, it hands back an empty string "" rather than complaining.
	cfg := Config{
		DBHost:     os.Getenv("DB_HOST"),
		DBPort:     os.Getenv("DB_PORT"),
		DBUser:     os.Getenv("DB_USER"),
		DBPassword: os.Getenv("DB_PASSWORD"),
		DBName:     os.Getenv("DB_NAME"),
		RedisAddr:  os.Getenv("REDIS_ADDR"),
		ServerPort: os.Getenv("SERVER_PORT"),
	}

	// Because a missing variable silently becomes "", we have to check for
	// empties ourselves. This map pairs each variable's name with the value
	// we actually got, so that the error message can say exactly which one
	// is missing instead of a vague "bad config".
	required := map[string]string{
		"DB_HOST":     cfg.DBHost,
		"DB_PORT":     cfg.DBPort,
		"DB_USER":     cfg.DBUser,
		"DB_PASSWORD": cfg.DBPassword,
		"DB_NAME":     cfg.DBName,
		"REDIS_ADDR":  cfg.RedisAddr,
		"SERVER_PORT": cfg.ServerPort,
	}

	// Walk through every required setting. The moment we find an empty one,
	// stop and report it. Failing here - at startup - is much better than
	// letting the program run and break confusingly later on.
	for name, value := range required {
		if value == "" {
			return Config{}, fmt.Errorf("missing environment variable: %s", name)
		}
	}

	// Everything is present. Hand the settings back, with no error.
	return cfg, nil
}
