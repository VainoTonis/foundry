package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const telemetryReceiptSelectColumns = `id, producer_id, event_id, client_seq, event_type, session, received_at`

func scanTelemetryReceipt(row pgx.Row) (TelemetryReceipt, error) {
	var r TelemetryReceipt
	err := row.Scan(&r.ID, &r.ProducerID, &r.EventID, &r.ClientSeq, &r.EventType, &r.Session, &r.ReceivedAt)
	return r, err
}

type InsertTelemetryReceiptParams struct {
	ProducerID string
	EventID    string
	ClientSeq  int64
	EventType  string
	Session    string
	ReceivedAt *time.Time
}

// InsertTelemetryReceipt records a producer-assigned event identity
// (ProducerID + EventID) in the telemetry receipt ledger. The insert is a
// single atomic statement backed by a UNIQUE (producer_id, event_id)
// constraint: if a receipt for that identity already exists, no row is
// inserted and inserted=false is returned instead of an error, so callers
// can treat the delivery as an idempotent duplicate rather than a failure.
func InsertTelemetryReceipt(ctx context.Context, pool querier, p InsertTelemetryReceiptParams) (receipt TelemetryReceipt, inserted bool, err error) {
	row := pool.QueryRow(ctx,
		`INSERT INTO telemetry_receipts (producer_id, event_id, client_seq, event_type, session, received_at)
		 VALUES ($1, $2, $3, $4, $5, COALESCE($6, now()))
		 ON CONFLICT (producer_id, event_id) DO NOTHING
		 RETURNING `+telemetryReceiptSelectColumns,
		p.ProducerID, p.EventID, p.ClientSeq, p.EventType, p.Session, p.ReceivedAt,
	)
	receipt, err = scanTelemetryReceipt(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return TelemetryReceipt{}, false, nil
	}
	if err != nil {
		return TelemetryReceipt{}, false, err
	}
	return receipt, true, nil
}
