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
	"time"
)

func main() {
	if err := run(); err != nil {
		slog.Error("np-uploader stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	tsAddr, tsKey := os.Getenv("NP_TITLE_STORAGE_ADDR"), os.Getenv("NP_TITLE_STORAGE_KEY")
	if tsAddr == "" || tsKey == "" {
		return fmt.Errorf("NP_TITLE_STORAGE_ADDR and NP_TITLE_STORAGE_KEY must be set")
	}
	if len(os.Getenv("NP_CORE_KEY")) < 32 {
		return fmt.Errorf("NP_CORE_KEY must be at least 32 bytes")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	storage, storageConn, err := DialStorage(tsAddr, tsKey)
	if err != nil {
		return err
	}
	defer storageConn.Close()
	store := &CoreStore{C: storage}
	// Declare the reserved title's limits to the core; Put works with the core's defaults until
	// this succeeds, so a core that is down at start only delays the stricter limits.
	go func() {
		for {
			c, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := store.Sync(c)
			cancel()
			if err == nil {
				return
			}
			log.Warn("title-storage policy sync", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
		}
	}()
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
	return nphttp.Run(ctx, mux, nphttp.Env("NP_HTTP_ADDR", ":8535"), os.Getenv("NP_TLS_ADDR"), front)
}
