# ServiceWatch
![version](https://img.shields.io/badge/version-2.0-blue)
![go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)
![platform](https://img.shields.io/badge/platform-Windows%20|%20Linux%20|%20macOS-lightgrey)
![license](https://img.shields.io/badge/license-MIT-green)

A cross-platform process monitor with auto-restart capabilities, a terminal UI dashboard, and a headless daemon mode.

![TUI Dashboard](screenshots/dashboard.png)

## Features

- **TUI Dashboard** — Real-time process status with live CPU, memory, and PID tracking
- **Headless Mode** — Run as a background daemon for unattended monitoring
- **Auto-Restart** — Automatically restart crashed processes with configurable cooldowns and retry limits
- **PID Pinning** — Tracks processes by PID with name-based fallback for reliable liveness detection
- **Process Picker** — Browse and filter all running system processes to add to your watchlist
- **Structured Logging** — JSONL event log with automatic rotation (10MB max, 5 backups, 7-day retention)
- **Cross-Platform** — Works on Windows, Linux, and macOS
- **Discord Alerts** — Optional Discord webhook feature to send alerts on process status change

## Quick Start

### Prerequisites

- Go 1.25+ (to build from source)

### Build & Run

```bash
git clone https://github.com/ethan-mdev/service-watch.git
cd service-watch
go build -o service-watch .
```

Launch the TUI:

```bash
./service-watch
```

Or run in headless/daemon mode:

```bash
./service-watch --headless
```

> **Note:** Headless mode requires an existing watchlist. Run the TUI first to set one up.

## Usage

### TUI Controls

| Key | Action |
|-----|--------|
| `a` | Add a process to the watchlist |
| `d` | Remove selected process (with confirmation) |
| `r` | Restart selected process (with confirmation) |
| `v` | Toggle debug info panel |
| `q` | Quit |

On startup, the TUI will prompt you to load an existing watchlist or start fresh.

![TUI Welcome](screenshots/welcome.png)

### Adding a Process

Press `a` to open the process picker, which lists all running system processes. Use `/` to filter. Select a process and configure its restart command, max retries, cooldown, and auto-restart setting.

![TUI Add Screen](screenshots/add.png)

### Configuration

`config.yaml` is created automatically on first run with sensible defaults:

```yaml
metricsPort: 9090
pollIntervalSecs: 5
restartVerifyDelaySecs: 3   # seconds to wait after restart before checking health
logLevel: info              # info | debug
alerts:
  enabled: false
  discordWebhookURL: ""     # ex: https://discord.com/api/webhooks/<id>/<token>
  projectLabel: "service-watch"
```

| Option | Description | Default |
|--------|-------------|---------|
| `metricsPort` | Port for metrics endpoint (planned) | `9090` |
| `pollIntervalSecs` | How often to check process status | `5` |
| `restartVerifyDelaySecs` | Delay after restart before verifying health | `3` |
| `logLevel` | Log verbosity (`info` or `debug`) | `info` |

### Discord Alerts

ServiceWatch can send real-time alerts to a Discord channel when monitored processes crash, fail to restart, or recover. Alerts are sent asynchronously and do not block the monitoring loop.

To enable alerts, edit the `alerts` section on your `config.yaml`:

```yaml
alerts:
  enabled: true
  discordWebhookURL: "https://discord.com/api/webhooks/YOUR_WEBHOOK_URL"
  projectLabel: "service-watch"
```

| Option | Description | Required |
|--------|-------------|----------|
| `enabled` | Enable/disable Discord alerting | Yes |
| `discordWebhookURL` | Discord webhook URL for alert delivery | Yes (if enabled) |
| `projectLabel` | Project label displayed in alert messages | No (defaults to `service-watch`) |

**Webhook URL:** Create a Discord webhook in your server's channel settings, then copy the full URL.

**Alert Events:**
- Process crashed and detected as down
- Restart command failed to execute
- Max retries exceeded (process permanently stopped)
- Restart verified successful (process recovered)

**Message Format:**

![Discord Alert Example](screenshots/discord-alert-example.png)

### Watchlist

The watchlist is stored in `watchlist.json` next to the executable. Each entry tracks:

- Process name and restart command
- Auto-restart toggle
- Max retries and cooldown period
- Restart/failure counters and last restart timestamp

### Logs

Events are logged to `logs/events.jsonl` in structured JSON format. Log rotation is automatic.

## Project Structure

```
service-watch/
├── main.go                        # Entry point, CLI flags, wiring
├── screenshots/                   # Screenshots for README 
└── internal/
    ├── config/config.go           # Config loading and validation
    ├── core/
    │   ├── types.go               # Core data types
    │   └── contracts.go           # Interfaces (ProcessManager, WatchlistManager)
    ├── logger/logger.go           # JSONL logger with rotation
    ├── monitor/watcher.go         # Polling loop, liveness checks, auto-restart
    ├── process/manager.go         # OS process operations via gopsutil
    ├── storage/watchlist.go       # JSON-backed watchlist persistence
    └── tui/
        ├── app.go                 # Bubble Tea bootstrap
        ├── model.go               # Top-level TUI model and routing
        └── views/
            ├── welcome.go         # Startup screen
            ├── list.go            # Main dashboard view
            └── picker.go          # Process picker and add form
```

## Roadmap

### v2.2 — Prometheus Metrics Endpoint
Expose process and host metrics at `/metrics` for scraping.

### v2.3 — TUI Log Viewer
Tail and filter structured logs directly within the TUI.

### v2.4 — Config Hot-Reload
Pick up `config.yaml` changes without restarting the application.

### v3 — Advanced TUI
Multi-stage failure policies, process grouping/tagging, and real-time/historical charts for process resource data.

## License

MIT License — see [LICENSE](LICENSE) for details.
