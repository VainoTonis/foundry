package db

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"
)

func InsertCerberusEvent(ctx context.Context, pool *pgxpool.Pool, session, eventType string, payload json.RawMessage) (CerberusEvent, error) {
	var e CerberusEvent
	err := pool.QueryRow(ctx,
		`INSERT INTO cerberus_events (session, event_type, payload) VALUES ($1, $2, $3)
		 RETURNING id, session, event_type, payload, created_at`,
		session, eventType, payload,
	).Scan(&e.ID, &e.Session, &e.EventType, &e.Payload, &e.CreatedAt)
	return e, err
}

func ListCerberusEvents(ctx context.Context, pool *pgxpool.Pool, session string, afterID int64) ([]CerberusEvent, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, session, event_type, payload, created_at
		 FROM cerberus_events WHERE session = $1 AND id > $2 ORDER BY id`,
		session, afterID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CerberusEvent
	for rows.Next() {
		var e CerberusEvent
		if err := rows.Scan(&e.ID, &e.Session, &e.EventType, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func DeleteCerberusEvents(ctx context.Context, pool *pgxpool.Pool, session string) error {
	_, err := pool.Exec(ctx, `DELETE FROM cerberus_events WHERE session = $1`, session)
	return err
}
