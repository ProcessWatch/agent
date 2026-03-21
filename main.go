package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ethan-mdev/service-watch/internal/alerting"
	"github.com/ethan-mdev/service-watch/internal/config"
	"github.com/ethan-mdev/service-watch/internal/core"
	"github.com/ethan-mdev/service-watch/internal/logger"
	"github.com/ethan-mdev/service-watch/internal/monitor"
	"github.com/ethan-mdev/service-watch/internal/process"
	"github.com/ethan-mdev/service-watch/internal/storage"
	"github.com/ethan-mdev/service-watch/internal/tui"
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

	var discordNotifier *alerting.DiscordNotifier
	if cfg.Alerts.Enabled {
		discordNotifier, err = alerting.NewDiscordNotifier(cfg.Alerts.DiscordWebhookURL)
		if err != nil {
			log.Fatalf("failed to initialize discord alerts: %v", err)
		}
		defer discordNotifier.Close()
	}

	// Context wired to OS signals
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Status channel — buffer of 4 so the initial poll never drops if the TUI
	// listener hasn't registered yet.
	statusCh := make(chan []core.WatchStatus, 4)

	// Watcher
	go monitor.Start(ctx, cfg, watchlist, processMgr, l, statusCh, discordNotifier)

	if *headless {
		items, err := watchlist.List(context.Background())
		if err != nil || len(items) == 0 {
			fmt.Fprintln(os.Stderr, "No watchlist found. Run without --headless to set up a watchlist using the TUI.")
			os.Exit(1)
		}

		l.Info("startup", map[string]interface{}{
			"mode":            "headless",
			"metricsEndpoint": fmt.Sprintf("http://localhost:%d/metrics (not yet enabled)", cfg.MetricsPort),
		})
		go func() {
			for range statusCh {
			}
		}()
		<-ctx.Done()
	} else {
		l.Info("startup", map[string]interface{}{"mode": "tui"})
		l.SetQuiet(true) // hand the terminal to the TUI
		if err := tui.Run(ctx, statusCh, watchlist, processMgr); err != nil {
			fmt.Fprintf(os.Stderr, "tui error: %v\n", err)
		}
		cancel()
	}

	l.Info("shutdown", nil)
}
