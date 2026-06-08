package authoring

import (
	"context"
	"log"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/cerberus"
	"github.com/tonis2/foundry/internal/db"
)

// RecoverOrphanDrafts marks active spec drafts as errored if their cerberus sessions
// are no longer running.
func RecoverOrphanDrafts(ctx context.Context, pool *pgxpool.Pool, cerb *cerberus.Client) {
	drafts, err := db.ListSpecDrafts(ctx, pool)
	if err != nil {
		log.Printf("orphan recovery: list drafts: %v", err)
		return
	}
	errStatus := "error"
	for _, d := range drafts {
		if d.Status != "active" {
			continue
		}
		if d.CerberusSession == "" {
			if _, updateErr := db.UpdateSpecDraft(ctx, pool, d.ID, db.UpdateSpecDraftParams{Status: &errStatus}); updateErr != nil {
				log.Printf("orphan recovery: mark draft %d error: %v", d.ID, updateErr)
			}
			continue
		}
		status, err := cerb.Status(ctx, d.CerberusSession)
		if err != nil || strings.Contains(status, "not found") || strings.Contains(status, "done") || strings.Contains(status, "failed") {
			log.Printf("orphan recovery: marking draft %d as error (status=%q err=%v)", d.ID, status, err)
			if _, updateErr := db.UpdateSpecDraft(ctx, pool, d.ID, db.UpdateSpecDraftParams{Status: &errStatus}); updateErr != nil {
				log.Printf("orphan recovery: mark draft %d error: %v", d.ID, updateErr)
			}
			continue
		}
		// session is alive (waiting) — leave it alone, user can resume from the UI
		if strings.Contains(status, "waiting") {
			log.Printf("orphan recovery: draft %d session %s is alive and waiting — keeping active", d.ID, d.CerberusSession)
		}
	}
}
