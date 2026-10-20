package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/kelpikz/shelf/server/internal/auth"
	"github.com/kelpikz/shelf/server/internal/config"
	"github.com/kelpikz/shelf/server/internal/server"
	"github.com/kelpikz/shelf/server/internal/sites"
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

	allSites := sites.New(db, cfg.DataDir)

	// "server publish <file or folder> <path>" makes a Site. It is for tests
	// until the CLI exists.
	if len(os.Args) > 1 && os.Args[1] == "publish" {
		if len(os.Args) != 4 {
			log.Fatal("usage: server publish <file or folder> <path>")
		}
		publish(cfg, allSites, os.Args[2], os.Args[3])
		return
	}

	if err := allSites.Cleanup(context.Background()); err != nil {
		log.Fatal(err)
	}

	github := auth.NewGitHub(cfg.GitHubClientID, cfg.GitHubClientSecret, cfg.AdminURL+"/auth/github/callback")
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           server.New(cfg, github, auth.NewSessions(db), allSites),
		ReadHeaderTimeout: 10 * time.Second,
		// The uploads will need more time. Give it to the upload handler
		// with http.ResponseController, not here.
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	log.Printf("shelf server listening on %s (sites: %s, admin: %s)", srv.Addr, cfg.FilesURL, cfg.AdminURL)
	log.Fatal(srv.ListenAndServe())
}

func publish(cfg config.Config, s *sites.Sites, local, path string) {
	ctx := context.Background()
	old, taken, err := s.Check(ctx, path)
	if err != nil {
		log.Fatalf("%s: %v", path, err)
	}
	site, err := s.PublishLocal(ctx, path, local)
	if err != nil {
		log.Fatal(err)
	}

	if taken {
		fmt.Println("Replaced the Site at", cfg.FilesURL+site.URL())
		fmt.Println("Browsers can show the old version for a while. Use Ctrl+F5 to see the new one.")
	} else {
		fmt.Println("Made a new Site at", cfg.FilesURL+site.URL())
	}
	fmt.Println("Visibility:", site.Visibility)
	if site.Visibility == sites.Private {
		fmt.Println("Make it public on the Admin page:", cfg.AdminURL)
	}
	if old.Archived {
		fmt.Println("The Site is archived, so it stays offline. Unarchive it on the Admin page.")
	}
}
