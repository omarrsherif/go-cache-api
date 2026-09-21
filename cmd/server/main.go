// Package main is the entry point of the program. Go runs the function called
// main() in the package called main when you start the app.
package main

import (
	// "log" prints messages with a timestamp, and can stop the program.
	"log"

	"github.com/omarrsherif/GO-PROJECT/internal/config"
	"github.com/omarrsherif/GO-PROJECT/internal/repo"
)

func main() {
	// Step 1: read the settings from .env (host, port, password, and so on).
	cfg, err := config.Load()
	if err != nil {
		// log.Fatalf prints the message and then exits the program straight
		// away. There is no point continuing without settings.
		log.Fatalf("config: %v", err)
	}

	// Step 2: connect to MySQL using those settings. This also pings the
	// database, so if we get past this line the connection genuinely works.
	db, err := repo.New(cfg)
	if err != nil {
		log.Fatalf("database: %v", err)
	}

	// "defer" schedules this to run when main() ends, no matter how it ends.
	// Writing it right next to the thing we opened is the usual Go habit, so
	// the cleanup is impossible to forget.
	defer db.Close()

	log.Println("connected to MySQL")

	// Next steps for this project: create the product repository with
	// repo.NewProductRepo(db), build the web handlers, and start the HTTP
	// server listening on cfg.ServerPort.
}
