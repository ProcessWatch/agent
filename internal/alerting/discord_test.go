package alerting

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewDiscordNotifierInvalidURL(t *testing.T) {
	t.Parallel()

	_, err := NewDiscordNotifier("not-a-url")
	if err == nil {
		t.Fatalf("NewDiscordNotifier() error = nil, want error")
	}
}

func TestDiscordNotifierSendsPayload(t *testing.T) {
	t.Parallel()

	reqCh := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		reqCh <- string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	n, err := NewDiscordNotifier(srv.URL)
	if err != nil {
		t.Fatalf("NewDiscordNotifier() returned error: %v", err)
	}
	defer n.Close()

	err = n.Notify(context.Background(), Event{
		Type:         EventProcessDown,
		ProcessName:  "api",
		ProjectLabel: "client-acme-prod",
		Message:      "process is down",
		Timestamp:    time.Now().UTC(),
		Host:         "droplet-1",
	})
	if err != nil {
		t.Fatalf("Notify() returned error: %v", err)
	}

	select {
	case body := <-reqCh:
		var payload struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatalf("invalid json payload: %v", err)
		}
		content := payload.Content
		if content == "" {
			t.Fatalf("content is empty")
		}
		if !strings.Contains(content, "@everyone") || !strings.Contains(content, "client-acme-prod") || !strings.Contains(content, "Process Down") || !strings.Contains(content, "Process : api") {
			t.Fatalf("unexpected payload content: %s", content)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for webhook request")
	}
}

func TestDiscordNotifierRetriesOnFailure(t *testing.T) {
	t.Parallel()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt32(&hits, 1)
		if current < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	n, err := NewDiscordNotifier(srv.URL)
	if err != nil {
		t.Fatalf("NewDiscordNotifier() returned error: %v", err)
	}
	defer n.Close()

	err = n.Notify(context.Background(), Event{Type: EventRestartFailed, ProcessName: "api"})
	if err != nil {
		t.Fatalf("Notify() returned error: %v", err)
	}

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&hits) >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Fatalf("webhook hit count = %d, want 3", got)
	}
}

func TestDiscordNotifierCloseThenNotify(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	n, err := NewDiscordNotifier(srv.URL)
	if err != nil {
		t.Fatalf("NewDiscordNotifier() returned error: %v", err)
	}

	if err := n.Close(); err != nil {
		t.Fatalf("Close() returned error: %v", err)
	}

	err = n.Notify(context.Background(), Event{Type: EventProcessDown})
	if !errors.Is(err, ErrNotifierClosed) {
		t.Fatalf("Notify() error = %v, want ErrNotifierClosed", err)
	}
}
