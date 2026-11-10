// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"path"
	"strings"

	"github.com/danielpang/dropway/internal/manifest"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/hosted"
	"github.com/danielpang/dropway/services/api/internal/store/db"
	"github.com/jackc/pgx/v5"
)

func (s *Store) ListExecutionJobs(ctx context.Context, t Tenant, roomID string) ([]ExecutionJob, error) {
	out := []ExecutionJob{}
	err := s.withTxRaw(ctx, t, func(tx pgx.Tx, q *db.Queries) error {
		if err := s.requireRooms(ctx, q, t.OrgID); err != nil {
			return err
		}
		if _, err := roomMember(ctx, q, t, roomID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+executionColumns+` FROM app.execution_jobs WHERE org_id=$1 AND room_id=$2 AND EXISTS(SELECT 1 FROM app.project_room_sites attached WHERE attached.org_id=execution_jobs.org_id AND attached.room_id=execution_jobs.room_id AND attached.site_id=execution_jobs.site_id) ORDER BY created_at DESC LIMIT 50`, t.OrgID, roomID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			j, err := scanExecution(rows)
			if err != nil {
				return err
			}
			out = append(out, j)
		}
		return rows.Err()
	})
	return out, err
}
func (s *Store) GetExecutionJob(ctx context.Context, t Tenant, roomID, jobID string) (ExecutionDetail, error) {
	var out ExecutionDetail
	err := s.withTxRaw(ctx, t, func(tx pgx.Tx, q *db.Queries) error {
		if err := s.requireRooms(ctx, q, t.OrgID); err != nil {
			return err
		}
		if _, err := roomMember(ctx, q, t, roomID); err != nil {
			return err
		}
		j, err := scanExecution(tx.QueryRow(ctx, `SELECT `+executionColumns+` FROM app.execution_jobs WHERE org_id=$1 AND room_id=$2 AND id=$3`, t.OrgID, roomID, jobID))
		if err != nil {
			return ErrNotFound
		}
		if _, err := q.GetProjectRoomSite(ctx, db.GetProjectRoomSiteParams{OrgID: t.OrgID, RoomID: roomID, SiteID: j.SiteID}); err != nil {
			return ErrNotFound
		}
		out.Job = j
		out.Events = []ExecutionEvent{}
		rows, err := tx.Query(ctx, `SELECT id::text,kind,message,created_at FROM app.execution_events WHERE org_id=$1 AND job_id=$2 ORDER BY created_at,id LIMIT 200`, t.OrgID, jobID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e ExecutionEvent
			if err := rows.Scan(&e.ID, &e.Kind, &e.Message, &e.CreatedAt); err != nil {
				return err
			}
			out.Events = append(out.Events, e)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()
		if j.SessionID != nil {
			return tx.QueryRow(ctx, `SELECT question FROM app.execution_sessions WHERE org_id=$1 AND id=$2`, t.OrgID, *j.SessionID).Scan(&out.Question)
		}
		return nil
	})
	return out, err
}
func (s *Store) CancelExecutionJob(ctx context.Context, t Tenant, roomID, jobID string) error {
	if !HumanSessionFromContext(ctx) {
		return hosted.ErrDenied
	}
	return s.withTxRaw(ctx, t, func(tx pgx.Tx, q *db.Queries) error {
		job, err := scanExecution(tx.QueryRow(ctx, `SELECT `+executionColumns+` FROM app.execution_jobs WHERE org_id=$1 AND room_id=$2 AND id=$3 FOR UPDATE`, t.OrgID, roomID, jobID))
		if err != nil {
			return ErrNotFound
		}
		if _, err := roomMember(ctx, q, t, roomID); err != nil {
			return err
		}
		if job.InitiatorUserID != t.UserID {
			return hosted.ErrDenied
		}
		if job.State == "completed" || job.State == "failed" || job.State == "cancelled" {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE app.execution_attempts SET state='cancelled',finished_at=now() WHERE org_id=$1 AND job_id=$2 AND state='running'`, t.OrgID, job.ID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE app.execution_jobs SET state='cancelled',active_attempt_id=NULL,updated_at=now() WHERE org_id=$1 AND id=$2`, t.OrgID, job.ID)
		if err != nil {
			return err
		}
		return recordHostedAudit(ctx, tx, t.OrgID, t.UserID, "execution.cancelled", "job:"+job.ID)
	})
}
func (s *Store) ClarifyExecution(ctx context.Context, token, question string, transcript json.RawMessage, files []hosted.File) (string, error) {
	var id string
	question = hosted.Redact(question)
	transcript = json.RawMessage(hosted.Redact(string(transcript)))
	if len(question) == 0 || len(question) > 4000 || len(transcript) > hosted.MaxTranscriptBytes || !json.Valid(transcript) {
		return "", hosted.ErrInvalid
	}
	if files != nil {
		if _, _, _, err := executionFiles(files, hosted.MaxFilesBytes); err != nil {
			return "", err
		}
	}
	err := s.withExecutionAttemptStates(ctx, token, "attempt", true, func(tx pgx.Tx, _ *db.Queries, a *AttemptContext) error {
		if files != nil {
			if _, _, _, err := executionFiles(files, a.Job.Config.Budget.MaxOutputBytes); err != nil {
				return err
			}
		}
		payload, _ := json.Marshal(map[string]any{"transcript": json.RawMessage(transcript), "files": files})
		if a.GatewayActive {
			return hosted.ErrConflict
		}
		if a.State == "clarification" {
			var priorQuestion string
			var priorPayload []byte
			if err := tx.QueryRow(ctx, `SELECT id::text,question,transcript FROM app.execution_sessions WHERE org_id=$1 AND attempt_id=$2`, a.Job.OrgID, a.AttemptID).Scan(&id, &priorQuestion, &priorPayload); err != nil {
				return err
			}
			var one, two any
			_ = json.Unmarshal(payload, &one)
			_ = json.Unmarshal(priorPayload, &two)
			if priorQuestion != question || hosted.Fingerprint(one) != hosted.Fingerprint(two) {
				return hosted.ErrConflict
			}
			return nil
		}

		if err := tx.QueryRow(ctx, `INSERT INTO app.execution_sessions(org_id,job_id,attempt_id,question,transcript) VALUES($1,$2,$3,$4,$5) RETURNING id::text`, a.Job.OrgID, a.Job.ID, a.AttemptID, question, payload).Scan(&id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.execution_attempts SET state='clarification',finished_at=now() WHERE org_id=$1 AND id=$2`, a.Job.OrgID, a.AttemptID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE app.execution_jobs SET state='clarification',session_id=$3,active_attempt_id=NULL,updated_at=now() WHERE org_id=$1 AND id=$2`, a.Job.OrgID, a.Job.ID, id)
		if err != nil {
			return err
		}
		return recordHostedAudit(ctx, tx, a.Job.OrgID, a.Job.InitiatorUserID, "execution.clarification", "job:"+a.Job.ID)
	})
	return id, err
}
func (s *Store) ResumeExecution(ctx context.Context, t Tenant, roomID, sessionID, answer, key string) (ExecutionJob, error) {
	var out ExecutionJob
	if !HumanSessionFromContext(ctx) || len(answer) < 1 || len(answer) > 16000 || len(key) < 8 || len(key) > 128 {
		return out, hosted.ErrInvalid
	}
	err := s.withTxRaw(ctx, t, func(tx pgx.Tx, q *db.Queries) error {
		var jobID string
		if err := tx.QueryRow(ctx, `SELECT job_id::text FROM app.execution_sessions WHERE org_id=$1 AND id=$2`, t.OrgID, sessionID).Scan(&jobID); err != nil {
			return ErrNotFound
		}
		j, err := scanExecution(tx.QueryRow(ctx, `SELECT `+executionColumns+` FROM app.execution_jobs WHERE org_id=$1 AND room_id=$2 AND id=$3 FOR UPDATE`, t.OrgID, roomID, jobID))
		if err != nil {
			return ErrNotFound
		}
		if j.InitiatorUserID != t.UserID {
			return hosted.ErrDenied
		}
		if err := s.authorizeExecution(ctx, tx, q, j); err != nil {
			return err
		}
		var priorKey, priorAnswer *string
		if err := tx.QueryRow(ctx, `SELECT resume_key,answer FROM app.execution_sessions WHERE org_id=$1 AND id=$2 FOR UPDATE`, t.OrgID, sessionID).Scan(&priorKey, &priorAnswer); err != nil {
			return err
		}
		if priorKey != nil {
			if *priorKey != key || priorAnswer == nil || *priorAnswer != answer {
				return hosted.ErrConflict
			}
			out = j
			return nil
		}
		if j.State != "clarification" || j.SessionID == nil || *j.SessionID != sessionID || j.AttemptCount >= 3 {
			return hosted.ErrConflict
		}
		if _, err := tx.Exec(ctx, `UPDATE app.execution_sessions SET answer=$3,resume_key=$4,resumed_at=now() WHERE org_id=$1 AND id=$2`, t.OrgID, sessionID, answer, key); err != nil {
			return err
		}
		out, err = scanExecution(tx.QueryRow(ctx, `UPDATE app.execution_jobs SET state='queued',updated_at=now() WHERE org_id=$1 AND id=$2 RETURNING `+executionColumns, t.OrgID, j.ID))
		if err != nil {
			return err
		}
		return recordHostedAudit(ctx, tx, t.OrgID, t.UserID, "execution.resumed", "job:"+j.ID)
	})
	return out, err
}

func executionFiles(files []hosted.File, limit int64) (map[string][]byte, manifest.Stored, string, error) {
	content := map[string][]byte{}
	stored := manifest.Stored{SchemaVersion: manifest.SchemaVersion, Files: map[string]manifest.Target{}}
	entries := []manifest.File{}
	var total int64
	if len(files) == 0 || len(files) > 500 {
		return nil, stored, "", hosted.ErrInvalid
	}
	for _, f := range files {
		if f.Path == "" || len(f.Path) > 1024 || strings.HasPrefix(f.Path, "/") || strings.ContainsAny(f.Path, "\\\x00\r\n") || path.Clean(f.Path) != f.Path || f.Path == "." || f.Path == ".." || strings.HasPrefix(f.Path, "../") || strings.HasPrefix(f.Path, ".git/") {
			return nil, stored, "", hosted.ErrInvalid
		}
		if _, ok := content[f.Path]; ok {
			return nil, stored, "", hosted.ErrInvalid
		}
		data, err := base64.StdEncoding.DecodeString(f.ContentBase64)
		if err != nil {
			return nil, stored, "", hosted.ErrInvalid
		}
		if len(data) > 2<<20 {
			return nil, stored, "", hosted.ErrBudget
		}
		total += int64(len(data))
		if total > limit {
			return nil, stored, "", hosted.ErrBudget
		}
		sha := hosted.Hash(string(data))
		mime := f.ContentType
		if mime == "" {
			mime = "application/octet-stream"
		}
		if len(mime) > 100 || strings.ContainsAny(mime, "\r\n") {
			return nil, stored, "", hosted.ErrInvalid
		}
		content[f.Path] = data
		stored.Files[f.Path] = manifest.Target{SHA256: sha, ContentType: mime, Size: int64(len(data))}
		entries = append(entries, manifest.File{Path: f.Path, SHA256: sha})
	}
	return content, stored, manifest.Digest(entries), nil
}
func (s *Store) ExecutionSource(ctx context.Context, token string, obj storage.Store) (ExecutionJob, []hosted.File, error) {
	var job ExecutionJob
	var files []hosted.File
	err := s.withExecutionAttempt(ctx, token, "attempt", func(tx pgx.Tx, _ *db.Queries, a *AttemptContext) error {
		job = a.Job
		if job.SessionID != nil {
			var raw []byte
			if err := tx.QueryRow(ctx, `SELECT transcript FROM app.execution_sessions WHERE org_id=$1 AND id=$2 AND resumed_at IS NOT NULL`, job.OrgID, *job.SessionID).Scan(&raw); err != nil {
				return err
			}
			var snapshot struct {
				Files []hosted.File `json:"files"`
			}
			if err := json.Unmarshal(raw, &snapshot); err != nil {
				return err
			}
			if snapshot.Files != nil {
				files = snapshot.Files
				return nil
			}
		}
		raw, err := obj.GetManifest(ctx, job.OrgID, job.SiteID, job.VersionID)
		if err != nil {
			return ErrCandidateArtifact
		}
		var m manifest.Stored
		if err := json.Unmarshal(raw, &m); err != nil {
			return ErrCandidateArtifact
		}
		if len(m.Files) > 500 {
			return hosted.ErrBudget
		}
		entries := []manifest.File{}
		var size int64
		for p, target := range m.Files {
			size += target.Size
			if target.Size < 0 || target.Size > 2<<20 || size > hosted.MaxFilesBytes {
				return hosted.ErrBudget
			}
			entries = append(entries, manifest.File{Path: p, SHA256: target.SHA256})
			rc, err := obj.GetBlob(ctx, job.OrgID, target.SHA256)
			if err != nil {
				return ErrCandidateArtifact
			}
			data, err := io.ReadAll(io.LimitReader(rc, target.Size+1))
			rc.Close()
			if err != nil || int64(len(data)) != target.Size || hosted.Hash(string(data)) != target.SHA256 {
				return ErrCandidateArtifact
			}
			files = append(files, hosted.File{Path: p, ContentType: target.ContentType, ContentBase64: base64.StdEncoding.EncodeToString(data), SHA256: target.SHA256, Size: target.Size})
		}
		if manifest.Digest(entries) != job.ContentHash {
			return ErrCandidateArtifact
		}
		if _, _, _, err := executionFiles(files, hosted.MaxFilesBytes); err != nil {
			return err
		}
		return nil
	})
	return job, files, err
}
func (s *Store) CompleteExecutionCandidate(ctx context.Context, token, key, summary string, files []hosted.File, obj storage.Store) (Candidate, error) {
	var out Candidate
	summary = hosted.Redact(summary)
	if len(key) < 8 || len(key) > 128 || len(strings.TrimSpace(summary)) == 0 || len(summary) > 2000 {
		return out, hosted.ErrInvalid
	}
	content, m, digest, err := executionFiles(files, hosted.MaxFilesBytes)
	if err != nil {
		return out, err
	}
	err = s.withExecutionAttemptStates(ctx, token, "attempt", true, func(tx pgx.Tx, q *db.Queries, a *AttemptContext) error {
		j := a.Job
		if a.State == "completed" {
			var priorKey, priorHash string
			if err := tx.QueryRow(ctx, `SELECT submission_key,submission_hash FROM app.execution_attempts WHERE org_id=$1 AND id=$2`, j.OrgID, a.AttemptID).Scan(&priorKey, &priorHash); err != nil {
				return err
			}
			if priorKey != key || priorHash != hosted.Fingerprint([]any{digest, m, summary}) || j.CandidateID == nil {
				return hosted.ErrConflict
			}
			row, err := q.GetReviewCandidate(ctx, db.GetReviewCandidateParams{OrgID: j.OrgID, RoomID: j.RoomID, ID: *j.CandidateID})
			if err != nil {
				return err
			}
			out = candidateFrom(row)
			return nil
		}

		if a.GatewayActive {
			return hosted.ErrConflict
		}
		if digest == j.ContentHash {
			return hosted.ErrInvalid
		}
		ver, lookupErr := q.GetSiteVersionByContentHash(ctx, db.GetSiteVersionByContentHashParams{SiteID: j.SiteID, OrgID: j.OrgID, ContentHash: digest})
		fresh := isNoRows(lookupErr)
		if lookupErr != nil && !fresh {
			return lookupErr
		}
		if !fresh {
			original, err := obj.GetManifest(ctx, j.OrgID, j.SiteID, ver.ID)
			if err != nil {
				return ErrCandidateArtifact
			}
			var canonical manifest.Stored
			if json.Unmarshal(original, &canonical) != nil || canonical.SchemaVersion != manifest.SchemaVersion || hosted.Fingerprint(canonical.Files) != hosted.Fingerprint(m.Files) {
				return ErrCandidateArtifact
			}
		}
		var size int64
		blobs := []BlobSize{}
		seen := map[string]bool{}
		for p, b := range content {
			target := m.Files[p]
			size += target.Size
			if size > j.Config.Budget.MaxOutputBytes {
				return hosted.ErrBudget
			}
			if !seen[target.SHA256] {
				seen[target.SHA256] = true
				blobs = append(blobs, BlobSize{SHA: target.SHA256, Size: target.Size})
			}
			exists, _, err := obj.HeadBlob(ctx, j.OrgID, target.SHA256)
			if err != nil {
				return err
			}
			if exists {
				rc, err := obj.GetBlob(ctx, j.OrgID, target.SHA256)
				if err != nil {
					return ErrCandidateArtifact
				}
				actual, err := io.ReadAll(io.LimitReader(rc, int64(len(b))+1))
				rc.Close()
				if err != nil || !bytes.Equal(actual, b) {
					return ErrCandidateArtifact
				}
			} else {
				if !fresh {
					return ErrCandidateArtifact
				}
				if err := obj.PutBlob(ctx, j.OrgID, target.SHA256, bytes.NewReader(b), int64(len(b)), target.ContentType); err != nil {
					return err
				}
			}
		}
		if fresh {
			if err := s.accountStorage(ctx, q, j.OrgID, blobs); err != nil {
				return err
			}
			no, err := q.NextVersionNo(ctx, db.NextVersionNoParams{OrgID: j.OrgID, SiteID: j.SiteID})
			if err != nil {
				return err
			}
			ver, err = q.CreateSiteVersion(ctx, db.CreateSiteVersionParams{OrgID: j.OrgID, SiteID: j.SiteID, VersionNo: no, Status: "ready", R2Prefix: manifestPrefix(j.OrgID, j.SiteID), ContentHash: digest, SizeBytes: size, CreatedBy: j.InitiatorUserID, CreatedVia: "deploy"})
			if err != nil {
				return err
			}
			raw, _ := json.Marshal(m)
			if err := obj.PutManifest(ctx, j.OrgID, j.SiteID, ver.ID, raw); err != nil {
				return err
			}
		}
		c, err := q.InsertReviewCandidate(ctx, db.InsertReviewCandidateParams{OrgID: j.OrgID, RoomID: j.RoomID, SiteID: j.SiteID, BaseVersionID: j.VersionID, BaseContentHash: j.ContentHash, ContentHash: digest, DiffHash: hosted.Hash(j.ContentHash + ":" + digest), Summary: summary, CreatedBy: j.InitiatorUserID})
		if err != nil {
			return err
		}
		out = candidateFrom(c)
		if err := writeActivity(ctx, q, Tenant{OrgID: j.OrgID, UserID: j.InitiatorUserID}, j.RoomID, "candidate_submitted", nil, &j.SiteID, &j.VersionID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.execution_attempts SET state='completed',finished_at=now(),submission_key=$3,submission_hash=$4 WHERE org_id=$1 AND id=$2`, j.OrgID, a.AttemptID, key, hosted.Fingerprint([]any{digest, m, summary})); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE app.execution_jobs SET state='completed',candidate_id=$3,active_attempt_id=NULL,updated_at=now() WHERE org_id=$1 AND id=$2`, j.OrgID, j.ID, c.ID)
		if err != nil {
			return err
		}
		return recordHostedAudit(ctx, tx, j.OrgID, j.InitiatorUserID, "execution.candidate_submitted", "candidate:"+c.ID)
	})
	return out, err
}
