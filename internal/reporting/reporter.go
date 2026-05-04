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

type hostPayload struct {
	Hostname         string  `json:"hostname"`
	OS               string  `json:"os"`
	Arch             string  `json:"arch"`
	AgentVersion     string  `json:"agentVersion"`
	CPUPercent       float64 `json:"cpuPercent"`
	MemPercent       float64 `json:"memPercent"`
	PollIntervalSecs int     `json:"pollIntervalSecs"`
}

type processPayload struct {
	Name          string  `json:"name"`
	PID           int32   `json:"pid"`
	Status        string  `json:"status"`
	CPUPercent    float64 `json:"cpuPercent"`
	MemMB         float64 `json:"memMB"`
	UptimeSeconds int64   `json:"uptimeSeconds"`
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

func (r *Reporter) Send(ctx context.Context, statuses []core.WatchStatus, events []core.ReportEvent, cpuPercent float64, memPercent float64, pollIntervalSecs int) error {
	payload := heartbeatPayload{
		APIKey: r.apiKey,
		Host: hostPayload{
			Hostname:         r.hostname,
			OS:               runtime.GOOS,
			Arch:             runtime.GOARCH,
			AgentVersion:     agentVersion,
			CPUPercent:       cpuPercent,
			MemPercent:       memPercent,
			PollIntervalSecs: pollIntervalSecs,
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
			Name:   s.Entry.Name,
			Status: "down",
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
