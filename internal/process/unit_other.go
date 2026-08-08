//go:build !linux

package process

import (
	"context"
	"fmt"

	"github.com/ethan-mdev/process-watch/internal/core"
)

// matchUnit is unavailable off Linux. Windows Service Control Manager support
// is on the roadmap; until then, use the exact or command-line match modes,
// which work on every platform.
func matchUnit(_ context.Context, pattern string) ([]core.Process, error) {
	return nil, fmt.Errorf("unit selector %q: systemd unit matching is only supported on Linux", pattern)
}
