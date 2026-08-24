package db

import (
	"context"
	"testing"
)

func TestInsertTelemetryReceipt_DuplicateEventID_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	producerID := "producer-dup-test"
	eventID := "event-dup-test-1"

	t.Cleanup(func() {
		if _, err := pool.Exec(ctx,
			`DELETE FROM telemetry_receipts WHERE producer_id = $1 AND event_id = $2`,
			producerID, eventID,
		); err != nil {
			t.Errorf("cleanup delete telemetry_receipts: %v", err)
		}
	})

	params := InsertTelemetryReceiptParams{
		ProducerID: producerID,
		EventID:    eventID,
		ClientSeq:  1,
		EventType:  "tool_use",
		Session:    "some-session",
	}

	first, inserted, err := InsertTelemetryReceipt(ctx, pool, params)
	if err != nil {
		t.Fatalf("InsertTelemetryReceipt() first call error = %v", err)
	}
	if !inserted {
		t.Fatal("InsertTelemetryReceipt() first call inserted = false, want true")
	}
	if first.ProducerID != producerID || first.EventID != eventID {
		t.Fatalf("first receipt = %+v, want producer_id=%q event_id=%q", first, producerID, eventID)
	}

	second, inserted, err := InsertTelemetryReceipt(ctx, pool, params)
	if err != nil {
		t.Fatalf("InsertTelemetryReceipt() second call error = %v", err)
	}
	if inserted {
		t.Fatal("InsertTelemetryReceipt() second call inserted = true, want false (duplicate event_id)")
	}
	if second != (TelemetryReceipt{}) {
		t.Fatalf("second receipt = %+v, want zero value for duplicate", second)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM telemetry_receipts WHERE producer_id = $1 AND event_id = $2`,
		producerID, eventID,
	).Scan(&count); err != nil {
		t.Fatalf("count telemetry_receipts: %v", err)
	}
	if count != 1 {
		t.Fatalf("telemetry_receipts row count = %d, want 1 (no duplicate rows)", count)
	}
}
