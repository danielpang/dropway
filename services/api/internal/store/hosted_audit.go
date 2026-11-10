package store

import (
	"context"
	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/services/api/internal/store/db"
	"github.com/jackc/pgx/v5"
)

// Hosted audit metadata deliberately excludes keys, prompts and transcripts.
func recordHostedAudit(ctx context.Context, tx pgx.Tx, org, user, action, target string) error {
	_, err := writeAuditTx(ctx, db.New(tx), org, AuditRecord{Ctx: audit.Context{ActorUser: user}, Action: audit.Action(action), Target: target})
	return err
}
