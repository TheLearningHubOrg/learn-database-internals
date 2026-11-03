// Command server runs the starter key-value store over HTTP.
//
//	PORT      listen port (default 8080)
//	DATA_DIR  directory for wal.log (default $TMPDIR/kvdb)
//	NO_SYNC   set to 1 to skip fsync per write (faster, less durable)
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/TheLearningHubOrg/learn-database-internals/internal/kv"
	"github.com/TheLearningHubOrg/learn-database-internals/internal/server"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	port := getenv("PORT", "8080")
	dir := getenv("DATA_DIR", filepath.Join(os.TempDir(), "kvdb"))

	start := time.Now()
	store, err := kv.Open(dir, kv.Options{NoSync: os.Getenv("NO_SYNC") == "1"})
	if err != nil {
		return err
	}
	defer store.Close()
	slog.Info("recovered", "dir", dir, "keys", store.Len(), "wal_bytes", store.LogSize(), "took", time.Since(start))

	srv := &http.Server{
		Addr:              net.JoinHostPort("0.0.0.0", port),
		Handler:           server.New(store),
		ReadHeaderTimeout: 5 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", srv.Addr)
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil // deferred store.Close() syncs the WAL
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
