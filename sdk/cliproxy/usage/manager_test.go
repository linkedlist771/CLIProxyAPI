package usage

import (
	"context"
	"testing"
)

type capturePlugin struct {
	records chan Record
}

func (p capturePlugin) HandleUsage(ctx context.Context, record Record) {
	p.records <- record
}

func TestStopDrainsQueuedRecords(t *testing.T) {
	records := make(chan Record, 1)
	manager := NewManager(1)
	manager.Register(capturePlugin{records: records})
	manager.Publish(context.Background(), Record{Model: "test-model"})
	manager.Stop()

	select {
	case record := <-records:
		if record.Model != "test-model" {
			t.Fatalf("record model = %q, want test-model", record.Model)
		}
	default:
		t.Fatalf("queued usage record was not drained before Stop returned")
	}
}

func TestStopBeforeStartDoesNotBlock(t *testing.T) {
	NewManager(1).Stop()
}
