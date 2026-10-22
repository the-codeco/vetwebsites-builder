package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"webbuilder/internal/config"
	"webbuilder/internal/platform"
)

// runPlatform serves the multi-website platform instead of a single website (PLATFORM=true).
func runPlatform(ctx context.Context, cfg config.Config, pool *pgxpool.Pool) {
	p, err := platform.New(ctx, cfg, pool)
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()
	go p.Run(ctx)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           p.Handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    32 << 10,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()
	log.Printf("platform listening on %s", cfg.Addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
