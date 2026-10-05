// Package repo ("repository") is the ONLY place in this project that talks to
// the database. Everything else asks this package for data instead of writing
// SQL itself. Keeping all the SQL in one folder means that if we ever change
// databases, this is the only folder we have to rewrite.
package repo

import (
	// "database/sql" is Go's built-in toolkit for talking to SQL databases.
	// On its own it does not know how to speak to MySQL specifically - it is
	// more like a universal remote that needs a matching device.
	"database/sql"

	// "fmt" is used for building strings and error messages.
	"fmt"

	// "time" lets us express durations, like "5 minutes".
	"time"

	// This is the MySQL "driver" - the piece that actually knows how to speak
	// MySQL's language. The underscore in front means "import this package
	// only for its side effects, we never call it by name". When it loads, it
	// quietly registers itself with database/sql under the name "mysql", which
	// is why sql.Open("mysql", ...) below works.
	_ "github.com/go-sql-driver/mysql"

	// Our own config package, which reads the database settings from .env.
	"github.com/omarrsherif/go-cache-api/internal/config"
)

// New opens the connection to MySQL and makes sure it actually works.
//
// It takes the settings we loaded from .env and gives back either a working
// database handle or an error explaining what went wrong.
//
// Call this ONCE when the program starts and pass the result around. Do not
// call it for every request - see the note about the pool further down.
func New(cfg config.Config) (*sql.DB, error) {
	// The DSN ("Data Source Name") is one long string that holds all the
	// details needed to reach the database. The format MySQL expects is:
	//
	//     user:password@tcp(host:port)/databasename?settings
	//
	// fmt.Sprintf fills in the %s placeholders with our real values in order.
	//
	// The three settings after the "?" are worth knowing:
	//
	//   parseTime=true       Without this, MySQL hands date columns back as raw
	//                        bytes and Go refuses to put them into a time.Time.
	//                        Turning it on means our CreatedAt/UpdatedAt fields
	//                        just work.
	//
	//   clientFoundRows=true Normally MySQL reports how many rows an UPDATE
	//                        *changed*. So saving a product without editing
	//                        anything reports "0 rows", which our code would
	//                        mistake for "this product does not exist". This
	//                        setting makes MySQL report how many rows it
	//                        *matched* instead, which is what we actually mean.
	//
	//   charset=utf8mb4      Match the character set the table was created with
	//                        so text and emoji survive the round trip.
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&clientFoundRows=true&charset=utf8mb4",
		cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort, cfg.DBName)

	// sql.Open prepares the connection. Despite the name, it does NOT actually
	// connect to anything yet - it only checks that the DSN text is shaped
	// correctly. An error here means the string is malformed, not that the
	// database is down.
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		// %w wraps the original error inside ours, so the detail is not lost.
		return nil, fmt.Errorf("open mysql: %w", err)
	}

	// What we got back is not a single connection - it is a "pool", a small
	// managed group of connections that Go hands out and reuses as needed.
	// These three lines set the size and lifetime of that pool.

	// Never hold more than 10 connections open at once. This protects the
	// database from being flooded if lots of requests arrive together.
	db.SetMaxOpenConns(10)

	// Keep up to 5 unused connections parked and ready. Reusing a parked
	// connection is much faster than opening a brand new one.
	db.SetMaxIdleConns(5)

	// Throw away and replace any connection older than 5 minutes. MySQL closes
	// idle connections on its own after a while; without this, Go could hand
	// our code a connection the server has already hung up on.
	db.SetConnMaxLifetime(5 * time.Minute)

	// Ping actually reaches out and talks to the database. This is the line
	// that tells us the truth: wrong password, wrong port, or MySQL not
	// running will all show up right here, at startup, instead of surprising
	// us later during a real request.
	if err := db.Ping(); err != nil {
		// Clean up the pool we just made before giving up, so we do not
		// leave connections dangling.
		db.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}

	// Everything worked. Hand the ready-to-use pool back to the caller.
	return db, nil
}
