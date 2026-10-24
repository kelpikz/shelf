package main

import (
	"log"
	"net/http"
	"time"

	"github.com/kelpikz/shelf/server/internal/admin"
	"github.com/kelpikz/shelf/server/internal/auth"
	"github.com/kelpikz/shelf/server/internal/config"
	"github.com/kelpikz/shelf/server/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	db, err := store.Open(cfg.DataDir)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	github := auth.NewGitHub(cfg.GitHubClientID, cfg.GitHubClientSecret, cfg.AdminURL+"/auth/github/callback")
	app := admin.New(cfg.OwnerEmail, cfg.OwnerGitHubID, cfg.AdminURL, github, auth.NewSessions(db))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})
	mux.Handle("/", app.Handler())

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		// The uploads will need more time. Give it to the upload handler
		// with http.ResponseController, not here.
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	log.Printf("shelf server listening on %s (admin: %s)", srv.Addr, cfg.AdminURL)
	log.Fatal(srv.ListenAndServe())
}
