package store

import (
	"context"
	"errors"
	"github.com/danielpang/dropway/services/api/internal/hosted"
	"github.com/danielpang/dropway/services/api/internal/store/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AIPreferences contains metadata only; provider secrets remain in the vault.
type AIPreferences struct {
	Mode          string `json:"mode"`
	CredentialRef string `json:"credential_ref"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
}

func (s *Store) GetAIPreferences(ctx context.Context, t Tenant, roomID string) (AIPreferences, error) {
	out := AIPreferences{Mode: "local"}
	err := s.withTxRaw(ctx, t, func(tx pgx.Tx, q *db.Queries) error {
		if _, err := credentialMember(ctx, tx, t); err != nil {
			return err
		}
		var err error
		if roomID != "" {
			if _, err = roomMember(ctx, q, t, roomID); err != nil {
				return err
			}
			err = tx.QueryRow(ctx, `SELECT provider,model FROM app.project_ai_preferences WHERE org_id=$1 AND room_id=$2`, t.OrgID, roomID).Scan(&out.Provider, &out.Model)
		} else {
			err = tx.QueryRow(ctx, `SELECT mode,COALESCE(credential_ref::text,''),provider,model FROM app.personal_ai_preferences WHERE org_id=$1 AND user_id=$2`, t.OrgID, t.UserID).Scan(&out.Mode, &out.CredentialRef, &out.Provider, &out.Model)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	return out, err
}
func (s *Store) SaveAIPreferences(ctx context.Context, t Tenant, roomID string, in AIPreferences) error {
	if !HumanSessionFromContext(ctx) {
		return hosted.ErrDenied
	}
	if in.CredentialRef != "" {
		if _, err := uuid.Parse(in.CredentialRef); err != nil {
			return hosted.ErrInvalid
		}
	}
	model := in.Model
	if in.Provider == "openrouter" {
		model = "anthropic/" + model
	}
	if in.Model != "" && !hosted.ModelAllowed(in.Provider, model) {
		return hosted.ErrInvalid
	}
	return s.withTxRaw(ctx, t, func(tx pgx.Tx, q *db.Queries) error {
		if _, err := credentialMember(ctx, tx, t); err != nil {
			return err
		}
		if roomID != "" {
			// Lock membership so an owner cannot change settings concurrently with removal.
			var role string
			if err := tx.QueryRow(ctx, `SELECT role FROM app.project_room_members WHERE org_id=$1 AND room_id=$2 AND user_id=$3 FOR SHARE`, t.OrgID, roomID, t.UserID).Scan(&role); err != nil || role != "owner" {
				return hosted.ErrDenied
			}
			if in.Model == "" {
				in.Provider = ""
			}
			_, err := tx.Exec(ctx, `INSERT INTO app.project_ai_preferences(org_id,room_id,provider,model) VALUES($1,$2,$3,$4) ON CONFLICT(org_id,room_id) DO UPDATE SET provider=EXCLUDED.provider,model=EXCLUDED.model`, t.OrgID, roomID, in.Provider, in.Model)
			return err
		}
		if in.Mode != "local" && in.Mode != "api" {
			return hosted.ErrInvalid
		}
		if in.Mode == "api" && (in.CredentialRef == "" || in.Model == "") {
			return hosted.ErrInvalid
		}
		if in.CredentialRef != "" {
			var provider string
			err := tx.QueryRow(ctx, `SELECT provider FROM app.provider_credentials WHERE org_id=$1 AND id=$2 AND revoked_at IS NULL AND (owner_user_id=$3 OR scope='team') FOR SHARE`, t.OrgID, in.CredentialRef, t.UserID).Scan(&provider)
			if err != nil || provider != in.Provider {
				return hosted.ErrDenied
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO app.personal_ai_preferences(org_id,user_id,mode,credential_ref,provider,model) VALUES($1,$2,$3,NULLIF($4,'')::uuid,$5,$6) ON CONFLICT(org_id,user_id) DO UPDATE SET mode=EXCLUDED.mode,credential_ref=EXCLUDED.credential_ref,provider=EXCLUDED.provider,model=EXCLUDED.model`, t.OrgID, t.UserID, in.Mode, in.CredentialRef, in.Provider, in.Model)
		return err
	})
}
