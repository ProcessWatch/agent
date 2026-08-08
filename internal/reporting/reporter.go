// Package reporting sends heartbeat payloads — host metrics, watched process
// states, and lifecycle events — to the hosted ProcessWatch dashboard ingest
// API. A failed send is logged and dropped; the next poll cycle reports the
// current state anyway, so there is no retry or queue.
package reporting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"time"

	"github.com/ethan-mdev/process-watch/internal/core"
)

const ingestURL = "https://app.processwatch.dev/api/ingest"
const agentVersion = "3.0.0"

// heartbeatPayload is the full payload POSTed to the ingest endpoint.
type heartbeatPayload struct {
	APIKey    string             `json:"apiKey"`
	Host      hostPayload        `json:"host"`
	Processes []processPayload   `json:"processes"`
	Events    []core.ReportEvent `json:"events"`
}

// ReportIntervalSecs tells the dashboard how often to expect a heartbeat, so
// it can decide when a host has gone quiet. It must be the reporting interval,
// not the local poll interval — those are now different numbers, and sending
// the faster one would have the dashboard declare healthy hosts down.
type hostPayload struct {
	Hostname           string  `json:"hostname"`
	OS                 string  `json:"os"`
	Arch               string  `json:"arch"`
	AgentVersion       string  `json:"agentVersion"`
	CPUPercent         float64 `json:"cpuPercent"`
	MemPercent         float64 `json:"memPercent"`
	ReportIntervalSecs int     `json:"reportIntervalSecs"`
}

// processPayload deliberately omits the matched process's command line.
// Command lines routinely carry API keys, database URLs and tokens passed as
// flags, and the dashboard has no use for them.
type processPayload struct {
	Name          string  `json:"name"`
	PID           int32   `json:"pid"`
	Status        string  `json:"status"`
	CPUPercent    float64 `json:"cpuPercent"`
	MemMB         float64 `json:"memMB"`
	UptimeSeconds int64   `json:"uptimeSeconds"`
	Found         int     `json:"found"`
	Expected      int     `json:"expected"`
}

// Reporter sends heartbeat payloads to the ProcessWatch ingest endpoint.
type Reporter struct {
	apiKey   string
	hostname string
	client   *http.Client
}

func NewReporter(apiKey string, hostname string) *Reporter {
	return &Reporter{
		apiKey:   apiKey,
		hostname: hostname,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (r *Reporter) Send(ctx context.Context, statuses []core.WatchStatus, events []core.ReportEvent, cpuPercent float64, memPercent float64, reportIntervalSecs int) error {
	payload := heartbeatPayload{
		APIKey: r.apiKey,
		Host: hostPayload{
			Hostname:           r.hostname,
			OS:                 runtime.GOOS,
			Arch:               runtime.GOARCH,
			AgentVersion:       agentVersion,
			CPUPercent:         cpuPercent,
			MemPercent:         memPercent,
			ReportIntervalSecs: reportIntervalSecs,
		},
		Processes: buildProcessPayloads(statuses),
		Events:    events,
	}

	if payload.Events == nil {
		payload.Events = []core.ReportEvent{}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("reporter: marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ingestURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("reporter: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// The dashboard currently reads the key from the body; also send it as a
	// bearer token so the body field can be dropped once ingest supports it.
	req.Header.Set("Authorization", "Bearer "+r.apiKey)

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("reporter: send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("reporter: ingest returned %d", resp.StatusCode)
	}

	return nil
}

func buildProcessPayloads(statuses []core.WatchStatus) []processPayload {
	procs := make([]processPayload, 0, len(statuses))
	for _, s := range statuses {
		p := processPayload{
			Name:     s.Entry.Name,
			Status:   "down",
			Found:    s.Found,
			Expected: s.Expected,
		}
		if s.Running && s.Process != nil {
			p.PID = s.Process.PID
			p.Status = "running"
			p.CPUPercent = s.Process.CPUPercent
			p.MemMB = s.Process.MemoryMB
			p.UptimeSeconds = s.Process.UptimeSeconds
		}
		if s.InCooldown {
			p.Status = "cooldown"
		}
		procs = append(procs, p)
	}
	return procs
}
