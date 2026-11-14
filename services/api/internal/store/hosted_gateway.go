// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package store

import (
	"context"
	"github.com/danielpang/dropway/services/api/internal/hosted"
	"github.com/danielpang/dropway/services/api/internal/store/db"
	"github.com/jackc/pgx/v5"
)

type GatewayGrant struct {
	Attempt         AttemptContext
	Secret          string
	MaxOutputTokens int64
	Reserved        int64
}

func (s *Store) ReserveGatewayCall(ctx context.Context, token string, inputBytes, maxOutput int64, vault *hosted.Vault) (GatewayGrant, error) {
	var grant GatewayGrant
	err := s.withExecutionAttempt(ctx, token, "gateway", func(tx pgx.Tx, _ *db.Queries, a *AttemptContext) error {
		j := a.Job
		if a.GatewayActive {
			return hosted.ErrConflict
		}
		remaining := j.Config.Budget.MaxTokens - j.TokensUsed
		input := inputBytes + 1024
		if maxOutput <= 0 || maxOutput > 8192 {
			maxOutput = 8192
		}
		if maxOutput > remaining-input {
			maxOutput = remaining - input
		}
		if maxOutput < 1 || j.CallsUsed >= j.Config.Budget.MaxCalls {
			return hosted.ErrBudget
		}
		reserved := input + maxOutput
		var ciphertext []byte
		var credentialOwner string
		if err := tx.QueryRow(ctx, `SELECT ciphertext,owner_user_id::text FROM app.provider_credentials WHERE org_id=$1 AND id=$2 AND (owner_user_id=$3 OR scope='team') AND generation=$4 AND revoked_at IS NULL AND validated_at IS NOT NULL`, j.OrgID, j.CredentialRef, j.InitiatorUserID, j.CredentialGeneration).Scan(&ciphertext, &credentialOwner); err != nil {
			return hosted.ErrDenied
		}
		secret, err := vault.Open(ciphertext, hosted.CredentialAAD(j.OrgID, credentialOwner, j.CredentialRef, j.Config.Provider))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.execution_attempts SET gateway_active=true,reserved_tokens=$3 WHERE org_id=$1 AND id=$2`, j.OrgID, a.AttemptID, reserved); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.execution_jobs SET tokens_used=tokens_used+$3,calls_used=calls_used+1,updated_at=now() WHERE org_id=$1 AND id=$2`, j.OrgID, j.ID, reserved); err != nil {
			return err
		}
		grant = GatewayGrant{Attempt: *a, Secret: secret, MaxOutputTokens: maxOutput, Reserved: reserved}
		return nil
	})
	return grant, err
}
func (s *Store) CheckGatewayCall(ctx context.Context, token string) error {
	return s.withExecutionAttempt(ctx, token, "gateway", func(_ pgx.Tx, _ *db.Queries, a *AttemptContext) error {
		if !a.GatewayActive {
			return hosted.ErrDenied
		}
		return nil
	})
}
func (s *Store) SettleGatewayCall(ctx context.Context, token string, tokens int64, valid bool) error {
	return s.withExecutionAttempt(ctx, token, "gateway", func(tx pgx.Tx, _ *db.Queries, a *AttemptContext) error {
		if !a.GatewayActive {
			return hosted.ErrConflict
		}
		if !valid || tokens < 0 || tokens > a.Reserved {
			if _, err := tx.Exec(ctx, `UPDATE app.execution_attempts SET state='failed',gateway_active=false,finished_at=now() WHERE org_id=$1 AND id=$2`, a.Job.OrgID, a.AttemptID); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `UPDATE app.execution_jobs SET state='failed',failure_code='usage_unverified',active_attempt_id=NULL,updated_at=now() WHERE org_id=$1 AND id=$2`, a.Job.OrgID, a.Job.ID)
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.execution_jobs SET tokens_used=tokens_used-$3+$4,updated_at=now() WHERE org_id=$1 AND id=$2`, a.Job.OrgID, a.Job.ID, a.Reserved, tokens); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE app.execution_attempts SET gateway_active=false,reserved_tokens=0 WHERE org_id=$1 AND id=$2`, a.Job.OrgID, a.AttemptID)
		return err
	})
}
