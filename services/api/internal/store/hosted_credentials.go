// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package store

import (
	"context"
	"github.com/danielpang/dropway/services/api/internal/hosted"
	"github.com/danielpang/dropway/services/api/internal/store/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
)

type ProviderCredential struct {
	ID          string     `json:"id"`
	Provider    string     `json:"provider"`
	Scope       string     `json:"scope"`
	OwnerUserID string     `json:"owner_user_id"`
	CanManage   bool       `json:"can_manage"`
	Label       string     `json:"label"`
	LastFour    string     `json:"last_four"`
	Generation  int        `json:"generation"`
	ValidatedAt *time.Time `json:"validated_at"`
	RevokedAt   *time.Time `json:"revoked_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

const credentialColumns = `id::text,provider,scope,owner_user_id::text,label,last_four,generation,validated_at,revoked_at,created_at`

func scanCredential(row pgx.Row) (c ProviderCredential, err error) {
	err = row.Scan(&c.ID, &c.Provider, &c.Scope, &c.OwnerUserID, &c.Label, &c.LastFour, &c.Generation, &c.ValidatedAt, &c.RevokedAt, &c.CreatedAt)
	return
}
func credentialMember(ctx context.Context, tx pgx.Tx, t Tenant) (string, error) {
	var role string
	err := tx.QueryRow(ctx, `SELECT role FROM identity.member WHERE "organizationId"=$1 AND "userId"=$2 FOR SHARE`, t.OrgID, t.UserID).Scan(&role)
	if err != nil {
		return "", hosted.ErrDenied
	}
	return role, nil
}
func canManageCredential(c ProviderCredential, t Tenant, role string) bool {
	if c.Scope == "team" {
		return IsAdminRole(role)
	}
	return c.OwnerUserID == t.UserID
}
func managedCredential(ctx context.Context, tx pgx.Tx, t Tenant, id string) (ProviderCredential, error) {
	role, err := credentialMember(ctx, tx, t)
	if err != nil {
		return ProviderCredential{}, err
	}
	c, err := scanCredential(tx.QueryRow(ctx, `SELECT `+credentialColumns+` FROM app.provider_credentials WHERE org_id=$1 AND id=$2 FOR UPDATE`, t.OrgID, id))
	if err != nil || !canManageCredential(c, t, role) {
		return ProviderCredential{}, hosted.ErrDenied
	}
	c.CanManage = true
	return c, nil
}
func (s *Store) ListProviderCredentials(ctx context.Context, t Tenant) ([]ProviderCredential, error) {
	out := []ProviderCredential{}
	err := s.withTxRaw(ctx, t, func(tx pgx.Tx, _ *db.Queries) error {
		role, err := credentialMember(ctx, tx, t)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+credentialColumns+` FROM app.provider_credentials WHERE org_id=$1 AND (owner_user_id=$2 OR scope='team') ORDER BY created_at DESC LIMIT 100`, t.OrgID, t.UserID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanCredential(rows)
			if err != nil {
				return err
			}
			c.CanManage = canManageCredential(c, t, role)
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

// The optional scope preserves existing personal-credential callers. Scope and
// creator never change; team replacements reuse the original creator in the AAD.
func (s *Store) SaveProviderCredential(ctx context.Context, t Tenant, id, provider, label, secret string, vault *hosted.Vault, scopes ...string) (ProviderCredential, error) {
	var out ProviderCredential
	scope := "personal"
	if len(scopes) > 0 && scopes[0] != "" {
		scope = scopes[0]
	}
	if !HumanSessionFromContext(ctx) || len(label) < 1 || len(label) > 80 || strings.TrimSpace(label) == "" || (provider != "anthropic" && provider != "openrouter" && provider != "openai") || (scope != "personal" && scope != "team") {
		return out, hosted.ErrDenied
	}
	label = strings.ReplaceAll(label, secret, "[redacted]")
	fresh := id == ""
	if fresh {
		id = uuid.NewString()
	}
	err := s.withTxRaw(ctx, t, func(tx pgx.Tx, _ *db.Queries) error {
		role, err := credentialMember(ctx, tx, t)
		if err != nil {
			return err
		}
		owner := t.UserID
		if !fresh {
			existing, err := managedCredential(ctx, tx, t, id)
			if err != nil {
				return err
			}
			if existing.Scope != scope || existing.Provider != provider {
				return hosted.ErrConflict
			}
			owner = existing.OwnerUserID
		} else if scope == "team" && !IsAdminRole(role) {
			return hosted.ErrDenied
		}
		sealed, err := vault.Seal(secret, hosted.CredentialAAD(t.OrgID, owner, id, provider))
		if err != nil {
			return err
		}
		if fresh {
			var count int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.provider_credentials WHERE org_id=$1 AND scope=$2 AND (scope='team' OR owner_user_id=$3) AND revoked_at IS NULL`, t.OrgID, scope, owner).Scan(&count); err != nil {
				return err
			}
			if count >= 20 {
				return hosted.ErrInvalid
			}
			_, err = tx.Exec(ctx, `INSERT INTO app.provider_credentials(id,org_id,owner_user_id,provider,label,last_four,ciphertext,scope) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, t.OrgID, owner, provider, label, secret[len(secret)-4:], sealed, scope)
		} else {
			_, err = tx.Exec(ctx, `UPDATE app.provider_credentials SET ciphertext=$3,label=$4,last_four=$5,generation=generation+1,validated_at=NULL,revoked_at=NULL WHERE org_id=$1 AND id=$2`, t.OrgID, id, sealed, label, secret[len(secret)-4:])
		}
		if err != nil {
			return err
		}
		out, err = scanCredential(tx.QueryRow(ctx, `SELECT `+credentialColumns+` FROM app.provider_credentials WHERE org_id=$1 AND id=$2`, t.OrgID, id))
		if err != nil {
			return err
		}
		out.CanManage = true
		return recordHostedAudit(ctx, tx, t.OrgID, t.UserID, "credential.save", "credential:"+id)
	})
	return out, err
}
func (s *Store) RevokeProviderCredential(ctx context.Context, t Tenant, id string) error {
	if !HumanSessionFromContext(ctx) {
		return hosted.ErrDenied
	}
	return s.withTxRaw(ctx, t, func(tx pgx.Tx, _ *db.Queries) error {
		if _, err := managedCredential(ctx, tx, t, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.provider_credentials SET revoked_at=COALESCE(revoked_at,now()),ciphertext=''::bytea,validated_at=NULL WHERE org_id=$1 AND id=$2`, t.OrgID, id); err != nil {
			return err
		}
		return recordHostedAudit(ctx, tx, t.OrgID, t.UserID, "credential.revoke", "credential:"+id)
	})
}

// ProviderCredentialSecret is management-only (provider validation). Worker use
// obtains secrets exclusively through the attempt-scoped gateway authorization.
func (s *Store) ProviderCredentialSecret(ctx context.Context, t Tenant, id string, vault *hosted.Vault) (provider, secret string, generation int, err error) {
	err = s.withTxRaw(ctx, t, func(tx pgx.Tx, _ *db.Queries) error {
		c, err := managedCredential(ctx, tx, t, id)
		if err != nil {
			return err
		}
		if c.RevokedAt != nil {
			return hosted.ErrDenied
		}
		provider, generation = c.Provider, c.Generation
		var cipher []byte
		if err := tx.QueryRow(ctx, `SELECT ciphertext FROM app.provider_credentials WHERE org_id=$1 AND id=$2`, t.OrgID, id).Scan(&cipher); err != nil {
			return err
		}
		secret, err = vault.Open(cipher, hosted.CredentialAAD(t.OrgID, c.OwnerUserID, id, provider))
		return err
	})
	return
}
func (s *Store) ValidateProviderCredential(ctx context.Context, t Tenant, id string, generation int) error {
	return s.withTxRaw(ctx, t, func(tx pgx.Tx, _ *db.Queries) error {
		c, err := managedCredential(ctx, tx, t, id)
		if err != nil {
			return err
		}
		if c.Generation != generation || c.RevokedAt != nil {
			return hosted.ErrConflict
		}
		if _, err := tx.Exec(ctx, `UPDATE app.provider_credentials SET validated_at=now() WHERE org_id=$1 AND id=$2`, t.OrgID, id); err != nil {
			return err
		}
		return recordHostedAudit(ctx, tx, t.OrgID, t.UserID, "credential.validate", "credential:"+id)
	})
}
