package workflow

import (
	"reflect"
	"testing"

	"github.com/tonis2/foundry/internal/db"
)

func TestPendingParallelBatchPreservesZeroBasedPositionOrder(t *testing.T) {
	one, two := 1, 2
	phases := []db.Phase{
		{ID: 10, Position: 0, Status: "done"},
		{ID: 11, Position: 1, Status: "pending", ParallelGroup: &one},
		{ID: 12, Position: 2, Status: "pending", ParallelGroup: &one},
		{ID: 13, Position: 3, Status: "pending", ParallelGroup: &two},
		{ID: 14, Position: 4, Status: "failed", ParallelGroup: &one},
	}

	got := pendingParallelBatch(phases, one)
	ids := make([]int64, 0, len(got))
	for _, phase := range got {
		ids = append(ids, phase.ID)
	}
	if want := []int64{11, 12}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("batch IDs = %v, want %v", ids, want)
	}
}
