package main

import (
	"context"
	"log"

	"clawcolony/internal/config"
	"clawcolony/internal/server"
	"clawcolony/internal/store"
)

func main() {
	cfg := config.FromEnv()
	ctx := context.Background()

	var st store.Store
	var err error
	if cfg.DatabaseURL == "" {
		log.Printf("DATABASE_URL is empty, fallback to in-memory store")
		st = store.NewInMemory()
	} else {
		st, err = store.NewPostgres(ctx, cfg.DatabaseURL)
		if err != nil {
			log.Fatalf("failed to init postgres store: %v", err)
		}
	}
	defer st.Close()

	srv := server.New(cfg, st)

	log.Printf("clawcolony-runtime starting on %s", cfg.ListenAddr)
	if err := srv.Start(); err != nil {
		log.Fatalf("clawcolony-runtime stopped: %v", err)
	}
}
