package alerting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	defaultRequestTimeout = 5 * time.Second
	defaultQueueSize      = 100
	defaultMaxRetries     = 2
	defaultRetryDelay     = 750 * time.Millisecond
)

var (
	ErrNotifierClosed = errors.New("alert notifier is closed")
	ErrQueueFull      = errors.New("alert queue is full")
)

type EventType string

const (
	EventProcessDown               EventType = "process_down"
	EventRestartFailed             EventType = "restart_failed"
	EventProcessMaxRetriesExceeded EventType = "process_max_retries_exceeded"
	EventRestartSuccess            EventType = "restart_success"
)

type Event struct {
	Type         EventType
	ProcessName  string
	ProjectLabel string
	Message      string
	Error        string
	Timestamp    time.Time
	Host         string
}

type DiscordNotifier struct {
	webhookURL string
	client     *http.Client

	maxRetries int
	retryDelay time.Duration
	queueSize  int

	mu      sync.RWMutex
	closed  bool
	closeCh chan struct{}
	queue   chan Event
	wg      sync.WaitGroup
}

func NewDiscordNotifier(webhookURL string) (*DiscordNotifier, error) {
	if err := validateWebhookURL(webhookURL); err != nil {
		return nil, err
	}

	n := &DiscordNotifier{
		webhookURL: webhookURL,
		client: &http.Client{
			Timeout: defaultRequestTimeout,
		},
		maxRetries: defaultMaxRetries,
		retryDelay: defaultRetryDelay,
		queueSize:  defaultQueueSize,
		closeCh:    make(chan struct{}),
	}

	n.queue = make(chan Event, n.queueSize)
	n.wg.Add(1)
	go n.worker()

	return n, nil
}

func (n *DiscordNotifier) Notify(ctx context.Context, event Event) error {
	n.mu.RLock()
	closed := n.closed
	n.mu.RUnlock()
	if closed {
		return ErrNotifierClosed
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-n.closeCh:
		return ErrNotifierClosed
	case n.queue <- event:
		return nil
	default:
		return ErrQueueFull
	}
}

func (n *DiscordNotifier) Close() error {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return nil
	}
	n.closed = true
	close(n.closeCh)
	close(n.queue)
	n.mu.Unlock()

	n.wg.Wait()
	return nil
}

func (n *DiscordNotifier) worker() {
	defer n.wg.Done()
	for event := range n.queue {
		_ = n.deliverWithRetry(event)
	}
}

func (n *DiscordNotifier) deliverWithRetry(event Event) error {
	var lastErr error
	for attempt := 0; attempt <= n.maxRetries; attempt++ {
		if err := n.sendOnce(event); err == nil {
			return nil
		} else {
			lastErr = err
		}

		if attempt < n.maxRetries {
			time.Sleep(n.retryDelay)
		}
	}
	return lastErr
}

func (n *DiscordNotifier) sendOnce(event Event) error {
	body := discordPayload{
		Content: formatDiscordContent(event),
		AllowedMentions: discordAllowedMentions{
			Parse: []string{"everyone"},
		},
	}
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal discord payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, n.webhookURL, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("build discord request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("post discord webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord webhook status: %d", resp.StatusCode)
	}
	return nil
}

func validateWebhookURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("invalid discord webhook URL")
	}
	return nil
}

type discordPayload struct {
	Content         string                 `json:"content"`
	AllowedMentions discordAllowedMentions `json:"allowed_mentions"`
}

type discordAllowedMentions struct {
	Parse []string `json:"parse,omitempty"`
}

func formatDiscordContent(event Event) string {
	ts := event.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	host := event.Host
	if strings.TrimSpace(host) == "" {
		host, _ = os.Hostname()
	}
	if host == "" {
		host = "unknown-host"
	}
	label := strings.TrimSpace(event.ProjectLabel)
	if label == "" {
		label = "service-watch"
	}
	title := eventTitle(event.Type)

	lines := []string{
		"@everyone",
		"```",
		"SERVICEWATCH INCIDENT",
		fmt.Sprintf("Project : %s", label),
		fmt.Sprintf("Event   : %s", title),
		fmt.Sprintf("Process : %s", valueOrUnknown(event.ProcessName)),
		fmt.Sprintf("Host    : %s", host),
		fmt.Sprintf("Time    : %s", ts.Format(time.RFC3339)),
	}
	if strings.TrimSpace(event.Message) != "" {
		lines = append(lines, fmt.Sprintf("Message : %s", strings.TrimSpace(event.Message)))
	}
	if strings.TrimSpace(event.Error) != "" {
		lines = append(lines, fmt.Sprintf("Error   : %s", strings.TrimSpace(event.Error)))
	}
	lines = append(lines, "```")
	return strings.Join(lines, "\n")
}

func eventTitle(eventType EventType) string {
	switch eventType {
	case EventProcessDown:
		return "Process Down"
	case EventRestartFailed:
		return "Restart Failed"
	case EventProcessMaxRetriesExceeded:
		return "Max Retries Exceeded"
	case EventRestartSuccess:
		return "Restart Recovered"
	default:
		return string(eventType)
	}
}

func valueOrUnknown(value string) string {
	v := strings.TrimSpace(value)
	if v == "" {
		return "unknown"
	}
	return v
}
