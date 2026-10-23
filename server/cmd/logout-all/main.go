// Command logout-all ends every Admin page session. Use it when no browser is
// logged in, for example after a lost laptop. It needs only DATA_DIR.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/kelpikz/shelf/server/internal/access"
	"github.com/kelpikz/shelf/server/internal/config"
	"github.com/kelpikz/shelf/server/internal/store"
)

func main() {
	dir, err := config.LoadDataDir()
	if err != nil {
		log.Fatal(err)
	}
	db, err := store.Open(dir)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	n, err := access.NewSessions(db).DeleteAll(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Ended %d Admin page sessions.\n", n)
}
