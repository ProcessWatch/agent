package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ethan-mdev/process-watch/internal/config"
	"github.com/ethan-mdev/process-watch/internal/core"
	"github.com/ethan-mdev/process-watch/internal/logger"
	"github.com/ethan-mdev/process-watch/internal/monitor"
	"github.com/ethan-mdev/process-watch/internal/process"
	"github.com/ethan-mdev/process-watch/internal/reporting"
	"github.com/ethan-mdev/process-watch/internal/storage"
	"github.com/ethan-mdev/process-watch/internal/tui"
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

	// Status channel — buffer of 4 so the initial poll never drops if the TUI
	// listener hasn't registered yet.
	statusCh := make(chan []core.WatchStatus, 4)

	// Reporter
	var reporter *reporting.Reporter
	if cfg.Reporting.Enabled {
		hostname, err := os.Hostname()
		if err != nil {
			hostname = "unknown"
		}
		reporter = reporting.NewReporter(cfg.Reporting.APIKey, hostname)
		l.Info("reporter_started", map[string]interface{}{
			"hostname": hostname,
		})
	}

	go monitor.Start(ctx, cfg, watchlist, processMgr, l, statusCh, reporter)

	if *headless {
		items, err := watchlist.List(context.Background())
		if err != nil || len(items) == 0 {
			fmt.Fprintln(os.Stderr, "No watchlist found. Run without --headless to set up a watchlist using the TUI.")
			os.Exit(1)
		}

		l.Info("startup", map[string]interface{}{
			"mode": "headless",
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
