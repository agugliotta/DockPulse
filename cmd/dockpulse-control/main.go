package main

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/dockpulse/dockpulse/internal/control"
	"github.com/dockpulse/dockpulse/internal/store"
	"github.com/dockpulse/dockpulse/internal/version"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	addr := env("DOCKPULSE_LISTEN_ADDR", ":8080")
	dbPath := env("DOCKPULSE_DB_PATH", "./dockpulse.db")
	key := os.Getenv("DOCKPULSE_ENCRYPTION_KEY")
	bootstrap := os.Getenv("DOCKPULSE_BOOTSTRAP_TOKEN")
	if key == "" || bootstrap == "" {
		log.Error("missing required DOCKPULSE_ENCRYPTION_KEY or DOCKPULSE_BOOTSTRAP_TOKEN")
		os.Exit(1)
	}
	st, err := store.Open(dbPath, key)
	if err != nil {
		log.Error("open_store", "error", err)
		os.Exit(1)
	}
	defer st.Close()
	cfg := control.Config{Version: version.Current, TargetVersion: env("DOCKPULSE_UPDATE_VERSION", env("DOCKPULSE_VERSION", "latest"))}
	srv := &http.Server{Addr: addr, Handler: control.New(st, bootstrap, env("DOCKPULSE_WEB_DIR", "./web/build"), log, cfg).Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 0, IdleTimeout: 60 * time.Second}
	log.Info("control_started", "addr", addr, "version", cfg.Version, "target_version", cfg.TargetVersion)
	if err = srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("server_stopped", "error", err)
		os.Exit(1)
	}
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
