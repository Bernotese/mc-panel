package main

import (
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent"
)

// Review K4: a graceful shutdown waits for a running console command, which the agent client
// bounds with agent.RconTimeout.
func TestShutdownOutlastsConsoleCommand(t *testing.T) {
	if shutdownTimeout <= agent.RconTimeout {
		t.Fatalf("shutdownTimeout %v must exceed agent.RconTimeout %v", shutdownTimeout, agent.RconTimeout)
	}
}
