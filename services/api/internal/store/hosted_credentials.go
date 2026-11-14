// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package store

import (
	"context"
	"strings"
	"time"

	"github.com/danielpang/dropway/services/api/internal/hosted"
	"github.com/danielpang/dropway/services/api/internal/store/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type ProviderCredential struct {
	ID          string     `json:"id"`
	Provider    string     `json:"provider"`
	Label       string     `json:"label"`
	LastFour    string     `json:"last_four"`
	Generation  int        `json:"generation"`
	ValidatedAt *time.Time `json:"validated_at"`
	RevokedAt   *time.Time `json:"revoked_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

func (s *Store) ListProviderCredentials(ctx context.Context, t Tenant) ([]ProviderCredential, error) {
	out := []ProviderCredential{}
	err := s.withTxRaw(ctx, t, func(tx pgx.Tx, _ *db.Queries) error {
		rows, err := tx.Query(ctx, `SELECT id::text,provider,label,last_four,generation,validated_at,revoked_at,created_at FROM app.provider_credentials WHERE org_id=$1 AND owner_user_id=$2 ORDER BY created_at DESC LIMIT 100`, t.OrgID, t.UserID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c ProviderCredential
			if err := rows.Scan(&c.ID, &c.Provider, &c.Label, &c.LastFour, &c.Generation, &c.ValidatedAt, &c.RevokedAt, &c.CreatedAt); err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}
func (s *Store) SaveProviderCredential(ctx context.Context, t Tenant, id, provider, label, secret string, vault *hosted.Vault) (ProviderCredential, error) {
	var out ProviderCredential
	if !HumanSessionFromContext(ctx) || len(label) < 1 || len(label) > 80 || strings.TrimSpace(label) == "" || (provider != "anthropic" && provider != "openrouter") {
		return out, hosted.ErrDenied
	}
	label = strings.ReplaceAll(label, secret, "[redacted]")
	fresh := id == ""
	if fresh {
		id = uuid.NewString()
	}
	sealed, err := vault.Seal(secret, hosted.CredentialAAD(t.OrgID, t.UserID, id, provider))
	if err != nil {
		return out, err
	}
	err = s.withTxRaw(ctx, t, func(tx pgx.Tx, _ *db.Queries) error {
		if fresh {
			var count int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.provider_credentials WHERE org_id=$1 AND owner_user_id=$2 AND revoked_at IS NULL`, t.OrgID, t.UserID).Scan(&count); err != nil {
				return err
			}
			if count >= 20 {
				return hosted.ErrInvalid
			}
			_, err := tx.Exec(ctx, `INSERT INTO app.provider_credentials(id,org_id,owner_user_id,provider,label,last_four,ciphertext) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, t.OrgID, t.UserID, provider, label, secret[len(secret)-4:], sealed)
			if err != nil {
				return err
			}
		} else {
			tag, err := tx.Exec(ctx, `UPDATE app.provider_credentials SET ciphertext=$4,label=$5,last_four=$6,generation=generation+1,validated_at=NULL,revoked_at=NULL WHERE id=$1 AND org_id=$2 AND owner_user_id=$3 AND provider=$7`, id, t.OrgID, t.UserID, sealed, label, secret[len(secret)-4:], provider)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return ErrNotFound
			}
		}
		err := tx.QueryRow(ctx, `SELECT id::text,provider,label,last_four,generation,validated_at,revoked_at,created_at FROM app.provider_credentials WHERE id=$1 AND org_id=$2 AND owner_user_id=$3`, id, t.OrgID, t.UserID).Scan(&out.ID, &out.Provider, &out.Label, &out.LastFour, &out.Generation, &out.ValidatedAt, &out.RevokedAt, &out.CreatedAt)
		if err != nil {
			return err
		}
		return recordHostedAudit(ctx, tx, t.OrgID, t.UserID, "credential.save", "credential:"+id)
	})
	return out, err
}
func (s *Store) RevokeProviderCredential(ctx context.Context, t Tenant, id string) error {
	if !HumanSessionFromContext(ctx) {
		return hosted.ErrDenied
	}
	return s.withTxRaw(ctx, t, func(tx pgx.Tx, _ *db.Queries) error {
		tag, err := tx.Exec(ctx, `UPDATE app.provider_credentials SET revoked_at=COALESCE(revoked_at,now()),ciphertext=''::bytea,validated_at=NULL WHERE id=$1 AND org_id=$2 AND owner_user_id=$3`, id, t.OrgID, t.UserID)
		if err == nil && tag.RowsAffected() != 1 {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return recordHostedAudit(ctx, tx, t.OrgID, t.UserID, "credential.revoke", "credential:"+id)
	})
}
func (s *Store) ProviderCredentialSecret(ctx context.Context, t Tenant, id string, vault *hosted.Vault) (string, string, int, error) {
	var provider, secret string
	var generation int
	err := s.withTxRaw(ctx, t, func(tx pgx.Tx, _ *db.Queries) error {
		var cipher []byte
		err := tx.QueryRow(ctx, `SELECT provider,ciphertext,generation FROM app.provider_credentials WHERE id=$1 AND org_id=$2 AND owner_user_id=$3 AND revoked_at IS NULL`, id, t.OrgID, t.UserID).Scan(&provider, &cipher, &generation)
		if isNoRows(err) {
			return hosted.ErrDenied
		}
		if err != nil {
			return err
		}
		secret, err = vault.Open(cipher, hosted.CredentialAAD(t.OrgID, t.UserID, id, provider))
		return err
	})
	return provider, secret, generation, err
}
func (s *Store) ValidateProviderCredential(ctx context.Context, t Tenant, id string, generation int) error {
	return s.withTxRaw(ctx, t, func(tx pgx.Tx, _ *db.Queries) error {
		tag, err := tx.Exec(ctx, `UPDATE app.provider_credentials SET validated_at=now() WHERE id=$1 AND org_id=$2 AND owner_user_id=$3 AND generation=$4 AND revoked_at IS NULL`, id, t.OrgID, t.UserID, generation)
		if err == nil && tag.RowsAffected() != 1 {
			return hosted.ErrConflict
		}
		if err != nil {
			return err
		}
		return recordHostedAudit(ctx, tx, t.OrgID, t.UserID, "credential.validate", "credential:"+id)
	})
}
