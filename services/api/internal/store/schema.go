// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// SchemaQuerier is satisfied by the runtime-role pool and transactions.
type SchemaQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// CheckSchema checks the schema contract with the actual runtime role. It never
// migrates, reads tenant data, or needs access to the owner-only goose ledger.
// Both the deployment release check and normal API startup call this before any
// HTTP listener or background worker starts. Old binaries remain safe to serve
// against the additive schema while a new release is rejected.
func CheckSchema(ctx context.Context, db SchemaQuerier) error {
	checks := []struct{ name, sql string }{
		{"0019 room flag", `SELECT project_rooms_enabled FROM app.org_meta LIMIT 0`},
		{"0019 API key resolver", `SELECT id,org_id,created_by,expires_at,revoked_at,org_status,api_keys_enabled,scopes,site_id FROM app.resolve_api_key('schema-check-not-a-key') LIMIT 0`},
		{"0019 room tables", `SELECT r.id,m.user_id,s.site_id,t.id,a.id,p.id,v.version_id,st.user_id,act.id,c.id FROM app.project_rooms r,app.project_room_members m,app.project_room_sites s,app.review_threads t,app.review_anchor_revisions a,app.review_replies p,app.review_version_approvals v,app.review_read_state st,app.review_activity act,app.review_candidates c LIMIT 0`},
		{"0020 candidate application", `SELECT a.candidate_id,o.deleted_preview_hosts,o.delivered_at FROM app.candidate_applications a,app.candidate_projection_outbox o LIMIT 0`},
		{"0021 hosted execution", `SELECT c.generation,j.config,a.gateway_hash,s.transcript,e.message FROM app.provider_credentials c,app.execution_jobs j,app.execution_attempts a,app.execution_sessions s,app.execution_events e LIMIT 0`},
		{"0022 projection scheduling", `SELECT 1 FROM app.candidate_projection_org_ids() LIMIT 0`},
		{"0023 shared credentials", `SELECT scope FROM app.provider_credentials LIMIT 0`},
		{"0024 mention execution", `SELECT trigger_reply_id FROM app.execution_jobs LIMIT 0`},
		{"0025 project guests", `SELECT id,room_id,email,role,token_hash,accepted_user_id,accepted_at,expires_at,revoked_at,last_invited_at FROM app.project_guest_shares LIMIT 0`},
		{"0025 guest discovery", `SELECT org_id,room_id FROM app.discover_guest_projects('00000000-0000-0000-0000-000000000000'::uuid,NULL,NULL) LIMIT 0`},
	}
	for _, check := range checks {
		rows, err := db.Query(ctx, check.sql)
		if err != nil {
			return fmt.Errorf("database schema preflight failed (%s): apply app migrations through 0025 with the migration role before deploying this API; runtime role must retain its grants", check.name)
		}
		rows.Close()
		if rows.Err() != nil {
			return fmt.Errorf("database schema preflight could not complete (%s); verify database availability and migrations through 0025", check.name)
		}
	}
	rows, err := db.Query(ctx, `SELECT has_function_privilege(current_user,'app.resolve_api_key(text)','EXECUTE') AND has_function_privilege(current_user,'app.candidate_projection_org_ids()','EXECUTE') AND has_function_privilege(current_user,'app.discover_guest_projects(uuid,text,uuid)','EXECUTE')`)
	if err != nil {
		return fmt.Errorf("database schema preflight could not verify runtime function grants")
	}
	defer rows.Close()
	var allowed bool
	if !rows.Next() || rows.Scan(&allowed) != nil || !allowed || rows.Err() != nil {
		return fmt.Errorf("database schema preflight failed: runtime role needs EXECUTE on API key resolver, projection scheduler and guest discovery; restore migration grants before deployment")
	}
	return nil
}
