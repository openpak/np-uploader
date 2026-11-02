// Command np-uploader stores PS3 message attachments (the firmware's "uploader" service:
// /uploader/sec/upload_data, download_data, delete_data).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"openpak/np-common/coreclient"
	"openpak/np-common/nphttp"
	"openpak/np-common/ticket"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		slog.Error("np-uploader stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	dbURL := os.Getenv("NP_UPLOADER_DATABASE_URL")
	if dbURL == "" {
		return fmt.Errorf("NP_UPLOADER_DATABASE_URL must be set")
	}
	if len(os.Getenv("NP_CORE_KEY")) < 32 {
		return fmt.Errorf("NP_CORE_KEY must be at least 32 bytes")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	store := &PGStore{DB: pool}
	if err := store.Migrate(ctx); err != nil {
		return err
	}
	core, err := coreclient.Dial(nphttp.Env("NP_CORE_ADDR", "localhost:7000"), os.Getenv("NP_CORE_KEY"))
	if err != nil {
		return err
	}
	defer core.Close()
	signer, err := ticket.Load(os.Getenv("NP_TICKET_KEY"))
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	(&Service{Core: core, Tickets: signer, Store: store, Log: log}).Register(mux)
	nphttp.Health(mux)
	front, err := nphttp.Front()
	if err != nil {
		return err
	}
	return nphttp.Run(ctx, mux, nphttp.Env("NP_HTTP_ADDR", ":8533"), os.Getenv("NP_TLS_ADDR"), front)
}
