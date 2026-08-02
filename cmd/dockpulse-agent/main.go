package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dockpulse/dockpulse/internal/agent"
)

const version = "0.1.0"

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	required := []string{"DOCKPULSE_AGENT_ID", "DOCKPULSE_AGENT_NAME", "DOCKPULSE_AGENT_URL", "DOCKPULSE_AGENT_SECRET", "DOCKPULSE_CONTROL_URL", "DOCKPULSE_BOOTSTRAP_TOKEN"}
	for _, k := range required {
		if os.Getenv(k) == "" {
			log.Error("missing_configuration", "key", k)
			os.Exit(1)
		}
	}
	if len(os.Getenv("DOCKPULSE_AGENT_SECRET")) < 32 {
		log.Error("agent secret must be at least 32 characters")
		os.Exit(1)
	}
	runner := agent.DockerCLI{Binary: env("DOCKPULSE_DOCKER_BINARY", "docker")}
	roots := strings.Split(env("DOCKPULSE_COMPOSE_ROOTS", "/opt/stacks,/srv/compose"), ",")
	cfg := agent.Config{ID: os.Getenv("DOCKPULSE_AGENT_ID"), Name: os.Getenv("DOCKPULSE_AGENT_NAME"), BaseURL: os.Getenv("DOCKPULSE_AGENT_URL"), Secret: os.Getenv("DOCKPULSE_AGENT_SECRET"), ControlURL: os.Getenv("DOCKPULSE_CONTROL_URL"), BootstrapToken: os.Getenv("DOCKPULSE_BOOTSTRAP_TOKEN"), Version: version, ReadOnly: env("DOCKPULSE_READ_ONLY", "true") == "true"}
	a := agent.New(cfg, agent.Discovery{Docker: runner, Registry: agent.NewRegistryClient(), AllowedComposeRoots: roots}, agent.Updater{Docker: runner, AllowedComposeRoots: roots}, log)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go a.RunControlLoop(ctx)
	srv := http.Server{Addr: env("DOCKPULSE_AGENT_LISTEN_ADDR", ":9090"), Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Info("agent_started", "id", cfg.ID, "addr", srv.Addr, "read_only", cfg.ReadOnly)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("agent_stopped", "error", err)
		os.Exit(1)
	}
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
