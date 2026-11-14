// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package store

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"math/rand/v2"
	"time"

	"github.com/danielpang/dropway/services/api/internal/hosted"
	"github.com/danielpang/dropway/services/api/internal/projectroom"
	"github.com/danielpang/dropway/services/api/internal/store/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type ExecutionJob struct {
	ID                   string        `json:"id"`
	OrgID                string        `json:"org_id"`
	RoomID               string        `json:"room_id"`
	SiteID               string        `json:"site_id"`
	VersionID            string        `json:"version_id"`
	ContentHash          string        `json:"content_hash"`
	InitiatorUserID      string        `json:"initiator_user_id"`
	CredentialRef        string        `json:"credential_ref"`
	CredentialGeneration int           `json:"credential_generation"`
	Config               hosted.Config `json:"config"`
	State                string        `json:"state"`
	AttemptCount         int           `json:"attempt_count"`
	TokensUsed           int64         `json:"tokens_used"`
	CallsUsed            int           `json:"calls_used"`
	CandidateID          *string       `json:"candidate_id"`
	SessionID            *string       `json:"session_id"`
	FailureCode          *string       `json:"failure_code"`
	CreatedAt            time.Time     `json:"created_at"`
	UpdatedAt            time.Time     `json:"updated_at"`
}
type ExecutionClaim struct {
	Job            ExecutionJob    `json:"job"`
	AttemptID      string          `json:"attempt_id"`
	AttemptToken   string          `json:"attempt_token"`
	GatewayToken   string          `json:"gateway_token"`
	LeaseExpiresAt time.Time       `json:"lease_expires_at"`
	Deadline       time.Time       `json:"deadline"`
	Transcript     json.RawMessage `json:"transcript,omitempty"`
	Answer         string          `json:"answer,omitempty"`
}
type ExecutionEvent struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}
type ExecutionDetail struct {
	Job      ExecutionJob     `json:"job"`
	Events   []ExecutionEvent `json:"events"`
	Question string           `json:"question,omitempty"`
}

const executionColumns = `id::text,org_id::text,room_id::text,site_id::text,version_id::text,content_hash,initiator_user_id::text,credential_id::text,credential_generation,config,state,attempt_count,tokens_used,calls_used,candidate_id::text,session_id::text,failure_code,created_at,updated_at`

func scanExecution(row pgx.Row) (ExecutionJob, error) {
	var j ExecutionJob
	var raw []byte
	err := row.Scan(&j.ID, &j.OrgID, &j.RoomID, &j.SiteID, &j.VersionID, &j.ContentHash, &j.InitiatorUserID, &j.CredentialRef, &j.CredentialGeneration, &raw, &j.State, &j.AttemptCount, &j.TokensUsed, &j.CallsUsed, &j.CandidateID, &j.SessionID, &j.FailureCode, &j.CreatedAt, &j.UpdatedAt)
	if err == nil {
		err = json.Unmarshal(raw, &j.Config)
	}
	return j, err
}

// authorizeExecution rechecks the initiating human's live identity membership,
// room role, site attachment/edit rights, credential generation, and baseline.
// All callers hold the job lock before calling; org/site locks are acquired here.
func (s *Store) authorizeExecution(ctx context.Context, tx pgx.Tx, q *db.Queries, j ExecutionJob) error {
	if s.collaborationAllowed == nil || !s.collaborationAllowed(ctx, j.InitiatorUserID, j.OrgID) {
		return hosted.ErrDenied
	}
	if _, err := lockOrgThenSite(ctx, q, j.OrgID, j.SiteID); err != nil {
		return hosted.ErrDenied
	}
	if err := s.requireRooms(ctx, q, j.OrgID); err != nil {
		return hosted.ErrDenied
	}
	var orgRole, roomRole string
	if err := tx.QueryRow(ctx, `SELECT role FROM identity.member WHERE "organizationId"=$1 AND "userId"=$2 FOR SHARE`, j.OrgID, j.InitiatorUserID).Scan(&orgRole); err != nil {
		return hosted.ErrDenied
	}
	if err := tx.QueryRow(ctx, `SELECT role FROM app.project_room_members WHERE org_id=$1 AND room_id=$2 AND user_id=$3 FOR SHARE`, j.OrgID, j.RoomID, j.InitiatorUserID).Scan(&roomRole); err != nil {
		return hosted.ErrDenied
	}
	site, err := q.GetSite(ctx, db.GetSiteParams{ID: j.SiteID, OrgID: j.OrgID})
	if err != nil {
		return hosted.ErrDenied
	}
	if !projectroom.CanRun(projectroom.Role(roomRole), projectroom.SiteRights{CanRead: true, CanEditContent: site.AllowMemberEdits || site.OwnerUserID == j.InitiatorUserID || IsAdminRole(orgRole)}) {
		return hosted.ErrDenied
	}
	if _, err := q.GetProjectRoomSite(ctx, db.GetProjectRoomSiteParams{OrgID: j.OrgID, RoomID: j.RoomID, SiteID: j.SiteID}); err != nil {
		return hosted.ErrDenied
	}
	if site.CurrentVersionID == nil || *site.CurrentVersionID != j.VersionID {
		return hosted.ErrConflict
	}
	version, err := q.GetSiteVersion(ctx, db.GetSiteVersionParams{ID: j.VersionID, OrgID: j.OrgID})
	if err != nil || version.SiteID != j.SiteID || version.ContentHash != j.ContentHash {
		return hosted.ErrConflict
	}
	if j.Config.ThreadID != "" {
		var bound bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app.review_threads WHERE org_id=$1 AND room_id=$2 AND site_id=$3 AND id=$4 AND version_id=$5 AND content_hash=$6)`, j.OrgID, j.RoomID, j.SiteID, j.Config.ThreadID, j.VersionID, j.ContentHash).Scan(&bound); err != nil {
			return err
		}
		if !bound {
			return hosted.ErrConflict
		}
	}
	var generation int
	var provider string
	if err := tx.QueryRow(ctx, `SELECT generation,provider FROM app.provider_credentials WHERE org_id=$1 AND id=$2 AND owner_user_id=$3 AND revoked_at IS NULL AND validated_at IS NOT NULL FOR SHARE`, j.OrgID, j.CredentialRef, j.InitiatorUserID).Scan(&generation, &provider); err != nil {
		return hosted.ErrDenied
	}
	if generation != j.CredentialGeneration || provider != j.Config.Provider {
		return hosted.ErrDenied
	}
	return nil
}
func (s *Store) QueueExecution(ctx context.Context, t Tenant, roomID, siteID, versionID, credentialID, key string, config hosted.Config) (ExecutionJob, error) {
	var out ExecutionJob
	if !HumanSessionFromContext(ctx) || len(key) < 8 || len(key) > 128 {
		return out, hosted.ErrInvalid
	}
	if err := config.Normalize(); err != nil {
		return out, err
	}
	fingerprint := hosted.Fingerprint([]any{roomID, siteID, versionID, credentialID, config})
	err := s.withTxRaw(ctx, t, func(tx pgx.Tx, q *db.Queries) error {
		// Tenant advisory lock serializes idempotency and queue caps without granting
		// a global queue lock across organizations.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, t.OrgID+":execution-queue"); err != nil {
			return err
		}
		existing, err := scanExecution(tx.QueryRow(ctx, `SELECT `+executionColumns+` FROM app.execution_jobs WHERE org_id=$1 AND initiator_user_id=$2 AND idempotency_key=$3 FOR UPDATE`, t.OrgID, t.UserID, key))
		if err == nil {
			var prior string
			if err := tx.QueryRow(ctx, `SELECT request_hash FROM app.execution_jobs WHERE org_id=$1 AND id=$2`, t.OrgID, existing.ID).Scan(&prior); err != nil {
				return err
			}
			if prior != fingerprint {
				return hosted.ErrConflict
			}
			if err := s.authorizeExecution(ctx, tx, q, existing); err != nil {
				return err
			}
			out = existing
			return nil
		}
		if !isNoRows(err) {
			return err
		}
		version, err := q.GetSiteVersion(ctx, db.GetSiteVersionParams{ID: versionID, OrgID: t.OrgID})
		if err != nil {
			return ErrNotFound
		}
		var generation int
		if err := tx.QueryRow(ctx, `SELECT generation FROM app.provider_credentials WHERE org_id=$1 AND id=$2 AND owner_user_id=$3 AND revoked_at IS NULL`, t.OrgID, credentialID, t.UserID).Scan(&generation); err != nil {
			return hosted.ErrDenied
		}
		out = ExecutionJob{OrgID: t.OrgID, RoomID: roomID, SiteID: siteID, VersionID: versionID, ContentHash: version.ContentHash, InitiatorUserID: t.UserID, CredentialRef: credentialID, CredentialGeneration: generation, Config: config}
		if err := s.authorizeExecution(ctx, tx, q, out); err != nil {
			return err
		}
		if config.ThreadID != "" {
			var valid bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app.review_threads WHERE org_id=$1 AND room_id=$2 AND site_id=$3 AND id=$4)`, t.OrgID, roomID, siteID, config.ThreadID).Scan(&valid); err != nil || !valid {
				return hosted.ErrDenied
			}
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.execution_jobs WHERE org_id=$1 AND initiator_user_id=$2 AND state IN ('queued','running','clarification')`, t.OrgID, t.UserID).Scan(&count); err != nil {
			return err
		}
		if count >= 5 {
			return hosted.ErrBudget
		}
		raw, _ := json.Marshal(config)
		out, err = scanExecution(tx.QueryRow(ctx, `INSERT INTO app.execution_jobs(org_id,room_id,site_id,version_id,content_hash,initiator_user_id,credential_id,credential_generation,idempotency_key,request_hash,config) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING `+executionColumns, t.OrgID, roomID, siteID, versionID, version.ContentHash, t.UserID, credentialID, generation, key, fingerprint, raw))
		if err != nil {
			return err
		}
		return recordHostedAudit(ctx, tx, t.OrgID, t.UserID, "execution.queued", "job:"+out.ID)
	})
	return out, err
}
func (s *Store) ClaimExecutionJob(ctx context.Context, worker string) (*ExecutionClaim, error) {
	orgs, err := s.ListAllOrgIDs(ctx)
	if err != nil {
		return nil, err
	}
	rand.Shuffle(len(orgs), func(i, j int) { orgs[i], orgs[j] = orgs[j], orgs[i] })
	for _, org := range orgs {
		var result *ExecutionClaim
		err := s.withTxRaw(ctx, Tenant{OrgID: org, UserID: org}, func(tx pgx.Tx, q *db.Queries) error {
			// Expired leases release the active slot; bounded retries get a new attempt.
			if _, err := tx.Exec(ctx, `WITH stale_jobs AS (SELECT j.id,j.active_attempt_id FROM app.execution_jobs j JOIN app.execution_attempts a ON a.id=j.active_attempt_id AND a.org_id=j.org_id WHERE j.org_id=$1 AND j.state='running' AND a.state='running' AND (a.lease_expires_at<=now() OR a.deadline<=now()) ORDER BY j.created_at LIMIT 100 FOR UPDATE OF j SKIP LOCKED), expired AS (UPDATE app.execution_attempts a SET state='expired',finished_at=now() FROM stale_jobs j WHERE a.org_id=$1 AND a.id=j.active_attempt_id AND a.state='running' RETURNING a.id,a.job_id) UPDATE app.execution_jobs j SET state=CASE WHEN attempt_count<max_attempts THEN 'queued' ELSE 'failed' END,active_attempt_id=NULL,failure_code='lease_expired',updated_at=now() FROM expired e WHERE j.org_id=$1 AND j.id=e.job_id AND j.active_attempt_id=e.id AND j.state='running'`, org); err != nil {
				return err
			}
			job, err := scanExecution(tx.QueryRow(ctx, `SELECT `+executionColumns+` FROM app.execution_jobs WHERE org_id=$1 AND state='queued' AND attempt_count<max_attempts ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1`, org))
			if isNoRows(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if err := s.authorizeExecution(ctx, tx, q, job); err != nil {
				_, err = tx.Exec(ctx, `UPDATE app.execution_jobs SET state='failed',failure_code='authorization_changed',updated_at=now() WHERE org_id=$1 AND id=$2`, org, job.ID)
				return err
			}
			if job.TokensUsed >= job.Config.Budget.MaxTokens || job.CallsUsed >= job.Config.Budget.MaxCalls {
				_, err = tx.Exec(ctx, `UPDATE app.execution_jobs SET state='failed',failure_code='budget_exhausted',updated_at=now() WHERE org_id=$1 AND id=$2`, org, job.ID)
				return err
			}
			id := uuid.NewString()
			attempt, attemptHash, err := hosted.Token("attempt", org, id)
			if err != nil {
				return err
			}
			gateway, gatewayHash, err := hosted.Token("gateway", org, id)
			if err != nil {
				return err
			}
			var elapsed float64
			if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(EXTRACT(EPOCH FROM (LEAST(COALESCE(finished_at,now()),deadline)-created_at))),0)::float8 FROM app.execution_attempts WHERE org_id=$1 AND job_id=$2`, org, job.ID).Scan(&elapsed); err != nil {
				return err
			}
			remaining := float64(job.Config.Budget.MaxSeconds) - elapsed
			if remaining <= 0 {
				_, err := tx.Exec(ctx, `UPDATE app.execution_jobs SET state='failed',failure_code='time_budget_exhausted',updated_at=now() WHERE org_id=$1 AND id=$2`, org, job.ID)
				return err
			}
			now := time.Now()
			lease := now.Add(hosted.LeaseDuration)
			deadline := now.Add(time.Duration(remaining * float64(time.Second)))
			if lease.After(deadline) {
				lease = deadline
			}
			if _, err := tx.Exec(ctx, `INSERT INTO app.execution_attempts(id,org_id,job_id,worker_id,token_hash,gateway_hash,state,lease_expires_at,deadline) VALUES($1,$2,$3,$4,$5,$6,'running',$7,$8)`, id, org, job.ID, worker, attemptHash, gatewayHash, lease, deadline); err != nil {
				return err
			}
			job, err = scanExecution(tx.QueryRow(ctx, `UPDATE app.execution_jobs SET state='running',active_attempt_id=$3,attempt_count=attempt_count+1,failure_code=NULL,updated_at=now() WHERE org_id=$1 AND id=$2 RETURNING `+executionColumns, org, job.ID, id))
			if err != nil {
				return err
			}
			if err := recordHostedAudit(ctx, tx, org, job.InitiatorUserID, "execution.claimed", "attempt:"+id); err != nil {
				return err
			}
			result = &ExecutionClaim{Job: job, AttemptID: id, AttemptToken: attempt, GatewayToken: gateway, LeaseExpiresAt: lease, Deadline: deadline}
			if job.SessionID != nil {
				if err := tx.QueryRow(ctx, `SELECT transcript,COALESCE(answer,'') FROM app.execution_sessions WHERE org_id=$1 AND id=$2 AND resumed_at IS NOT NULL`, org, *job.SessionID).Scan(&result.Transcript, &result.Answer); err != nil {
					return err
				}
				var saved struct {
					Transcript json.RawMessage `json:"transcript"`
				}
				if err := json.Unmarshal(result.Transcript, &saved); err != nil {
					return err
				}
				result.Transcript = saved.Transcript
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		if result != nil {
			return result, nil
		}
	}
	return nil, nil
}

type AttemptContext struct {
	State                    string
	Job                      ExecutionJob
	AttemptID                string
	Deadline, LeaseExpiresAt time.Time
	GatewayActive            bool
	Reserved                 int64
}

func (s *Store) withExecutionAttempt(ctx context.Context, token, kind string, fn func(pgx.Tx, *db.Queries, *AttemptContext) error) error {
	return s.withExecutionAttemptStates(ctx, token, kind, false, fn)
}
func (s *Store) withExecutionAttemptStates(ctx context.Context, token, kind string, terminal bool, fn func(pgx.Tx, *db.Queries, *AttemptContext) error) error {
	org, id, err := hosted.ParseToken(token, kind)
	if err != nil {
		return err
	}
	return s.withTxRaw(ctx, Tenant{OrgID: org, UserID: org}, func(tx pgx.Tx, q *db.Queries) error {
		var jobID string
		if err := tx.QueryRow(ctx, `SELECT job_id::text FROM app.execution_attempts WHERE org_id=$1 AND id=$2`, org, id).Scan(&jobID); err != nil {
			return hosted.ErrDenied
		}
		job, err := scanExecution(tx.QueryRow(ctx, `SELECT `+executionColumns+` FROM app.execution_jobs WHERE org_id=$1 AND id=$2 FOR UPDATE`, org, jobID))
		if err != nil {
			return hosted.ErrDenied
		}
		var hash, state string
		var active *string
		a := AttemptContext{Job: job, AttemptID: id}
		column := "token_hash"
		if kind == "gateway" {
			column = "gateway_hash"
		}
		if err := tx.QueryRow(ctx, `SELECT `+column+`,state,lease_expires_at,deadline,gateway_active,reserved_tokens FROM app.execution_attempts WHERE org_id=$1 AND id=$2 FOR UPDATE`, org, id).Scan(&hash, &state, &a.LeaseExpiresAt, &a.Deadline, &a.GatewayActive, &a.Reserved); err != nil {
			return hosted.ErrDenied
		}
		if err := tx.QueryRow(ctx, `SELECT active_attempt_id::text FROM app.execution_jobs WHERE org_id=$1 AND id=$2`, org, job.ID).Scan(&active); err != nil {
			return err
		}
		a.State = state
		running := job.State == "running" && state == "running" && active != nil && *active == id && time.Now().Before(a.LeaseExpiresAt) && time.Now().Before(a.Deadline)
		completed := terminal && ((state == "completed" && job.State == "completed") || (state == "clarification" && job.State == "clarification")) && time.Now().Before(a.Deadline)
		if subtle.ConstantTimeCompare([]byte(hash), []byte(hosted.Hash(token))) != 1 || (!running && !completed) {
			return hosted.ErrDenied
		}
		if err := s.authorizeExecution(ctx, tx, q, job); err != nil {
			return err
		}
		return fn(tx, q, &a)
	})
}
func (s *Store) ExecutionAttempt(ctx context.Context, token string) (AttemptContext, error) {
	var out AttemptContext
	err := s.withExecutionAttempt(ctx, token, "attempt", func(_ pgx.Tx, _ *db.Queries, a *AttemptContext) error { out = *a; return nil })
	return out, err
}
func (s *Store) HeartbeatExecution(ctx context.Context, token string) (time.Time, error) {
	var lease time.Time
	err := s.withExecutionAttempt(ctx, token, "attempt", func(tx pgx.Tx, _ *db.Queries, a *AttemptContext) error {
		return tx.QueryRow(ctx, `UPDATE app.execution_attempts SET lease_expires_at=LEAST(now()+interval '90 seconds',deadline) WHERE org_id=$1 AND id=$2 RETURNING lease_expires_at`, a.Job.OrgID, a.AttemptID).Scan(&lease)
	})
	return lease, err
}
func (s *Store) AppendExecutionEvent(ctx context.Context, token, kind, message string) error {
	message = hosted.Redact(message)
	kind = hosted.Redact(kind)
	if len(message) > 4000 || len(kind) > 40 || len(message) == 0 {
		return hosted.ErrInvalid
	}
	return s.withExecutionAttempt(ctx, token, "attempt", func(tx pgx.Tx, _ *db.Queries, a *AttemptContext) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.execution_events WHERE org_id=$1 AND job_id=$2`, a.Job.OrgID, a.Job.ID).Scan(&count); err != nil {
			return err
		}
		if count >= 200 {
			return hosted.ErrBudget
		}
		_, err := tx.Exec(ctx, `INSERT INTO app.execution_events(org_id,job_id,attempt_id,kind,message) VALUES($1,$2,$3,$4,$5)`, a.Job.OrgID, a.Job.ID, a.AttemptID, kind, message)
		return err
	})
}
func (s *Store) FailExecution(ctx context.Context, token, code string) error {
	code = hosted.Redact(code)
	if len(code) > 80 {
		return hosted.ErrInvalid
	}
	return s.withExecutionAttempt(ctx, token, "attempt", func(tx pgx.Tx, _ *db.Queries, a *AttemptContext) error {
		if _, err := tx.Exec(ctx, `UPDATE app.execution_attempts SET state='failed',finished_at=now() WHERE org_id=$1 AND id=$2`, a.Job.OrgID, a.AttemptID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE app.execution_jobs SET state='failed',failure_code=$3,active_attempt_id=NULL,updated_at=now() WHERE org_id=$1 AND id=$2`, a.Job.OrgID, a.Job.ID, code)
		return err
	})
}
