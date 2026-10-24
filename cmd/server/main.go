package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"webbuilder/internal/app"
	"webbuilder/internal/config"
	"webbuilder/internal/db"
)

func main() {
	// "server -healthcheck" is what Docker runs: the image has no shell or curl.
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		os.Exit(healthcheck())
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	if len(os.Args) > 1 && os.Args[1] == "-adopt-single-site" {
		code := adopt(ctx, cfg, pool, os.Args[2:])
		pool.Close()
		os.Exit(code)
	}

	if cfg.Platform {
		runPlatform(ctx, cfg, pool) // its own tables and one schema per website; never the single-site tables
		return
	}

	if err := db.Migrate(pool); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	a, err := app.New(ctx, cfg, pool)
	if err != nil {
		log.Fatal(err)
	}
	defer a.Close()
	go a.Maintain(ctx)
	go a.SendMail(ctx)
	go a.RunBackups(ctx)
	go a.Publish(ctx)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           a.Handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute, // generous: a 100 MB upload on a slow line
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    32 << 10,
	}

	// The proxy's certificate check lives on its own listener so it is never
	// reachable through the public port: Caddy talks to it over the private
	// Docker network.
	var internal *http.Server
	if cfg.InternalAddr != "" {
		internal = &http.Server{
			Addr: cfg.InternalAddr, Handler: a.Internal,
			ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
			WriteTimeout: 10 * time.Second, MaxHeaderBytes: 8 << 10,
		}
		go func() {
			log.Printf("internal listener on %s", cfg.InternalAddr)
			if err := internal.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				log.Fatalf("internal listener: %v", err)
			}
		}()
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
		if internal != nil {
			internal.Shutdown(shutdownCtx)
		}
	}()

	log.Printf("listening on %s", cfg.Addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// healthcheck asks the running server's /healthz and returns an exit code.
func healthcheck() int {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
