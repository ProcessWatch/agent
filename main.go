package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ethan-mdev/service-watch/internal/config"
	"github.com/ethan-mdev/service-watch/internal/core"
	"github.com/ethan-mdev/service-watch/internal/logger"
	"github.com/ethan-mdev/service-watch/internal/monitor"
	"github.com/ethan-mdev/service-watch/internal/process"
	"github.com/ethan-mdev/service-watch/internal/storage"
)

func main() {
	headless := flag.Bool("headless", false, "Run without TUI (daemon mode)")
	configPath := flag.String("config", "config.yaml", "Path to config file")
	flag.Parse()

	// Config
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	// Logger
	if err := os.MkdirAll("logs", 0755); err != nil {
		log.Fatalf("failed to create logs directory: %v", err)
	}
	l, err := logger.Start("logs/events.jsonl", cfg.LogLevel)
	if err != nil {
		log.Fatalf("failed to start logger: %v", err)
	}
	defer l.Close()

	// Storage & process manager
	watchlist := storage.NewJSONWatchlist("watchlist.json")
	processMgr := process.NewProcessManager()

	// Context wired to OS signals
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Status channel (buffered so watcher never blocks on a slow consumer)
	statusCh := make(chan []core.WatchStatus, 1)

	// Watcher
	go monitor.Start(ctx, cfg, watchlist, processMgr, l, statusCh, cfg.DiscordWebhook)

	if *headless {
		l.Info("startup", map[string]interface{}{
			"mode":            "headless",
			"metricsEndpoint": fmt.Sprintf("http://localhost:%d/metrics (not yet enabled)", cfg.MetricsPort),
		})
	} else {
		// TODO: tui.Run(ctx, statusCh, watchlist, processMgr)
		l.Info("startup", map[string]interface{}{
			"mode": "headless (TUI not yet implemented — use --headless)",
		})
	}

	// Drain status channel so the watcher never blocks until TUI/Prometheus consume it.
	go func() {
		for range statusCh {
		}
	}()

	<-ctx.Done()
	l.Info("shutdown", nil)
}
