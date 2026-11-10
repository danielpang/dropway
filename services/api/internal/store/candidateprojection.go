// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"

	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/services/api/internal/store/db"
	"github.com/jackc/pgx/v5"
)

// FlushCandidateProjection delivers one durable intent. Delivery holds the same
// org/site locks as publish and access updates, and derives the CURRENT route.
// A delayed job can never republish an old candidate or an old public policy.
// Errors roll back only the acknowledgment; the application is already durable.
func (s *Store) FlushCandidateProjection(ctx context.Context, t Tenant, candidateID string, w projection.Writer) error {
	return s.withTxRaw(ctx, t, func(tx pgx.Tx, q *db.Queries) error {
		var siteID string
		err := tx.QueryRow(ctx, `SELECT site_id::text FROM app.candidate_projection_outbox WHERE org_id=$1 AND candidate_id=$2`, t.OrgID, candidateID).Scan(&siteID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		site, err := lockOrgThenSite(ctx, q, t.OrgID, siteID)
		if err != nil {
			return err
		}
		var raw []byte
		err = tx.QueryRow(ctx, `SELECT deleted_preview_hosts FROM app.candidate_projection_outbox WHERE org_id=$1 AND candidate_id=$2 FOR UPDATE`, t.OrgID, candidateID).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if site.CurrentVersionID == nil {
			return fmt.Errorf("candidate projection: live version missing")
		}
		ver, err := q.GetSiteVersion(ctx, db.GetSiteVersionParams{ID: *site.CurrentVersionID, OrgID: t.OrgID})
		if err != nil {
			return err
		}
		res, err := s.publishLocked(ctx, tx, q, t, site, ver, false)
		if err != nil {
			return err
		}
		for _, route := range res.Routes {
			if err := w.PutRoute(ctx, route.Host, route.Route); err != nil {
				return err
			}
		}
		var previews []string
		if err := json.Unmarshal(raw, &previews); err != nil {
			return err
		}
		for _, host := range previews {
			// A preview may have been explicitly recreated since this application.
			// Its current reservation wins over this old cleanup intent.
			_, err := q.GetHostRoute(ctx, db.GetHostRouteParams{Host: host, OrgID: t.OrgID})
			if err == nil {
				continue
			}
			if !isNoRows(err) {
				return err
			}
			if err := w.DeleteRoute(ctx, host); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE app.candidate_projection_outbox SET delivered_at=now(),deleted_preview_hosts='[]'::jsonb WHERE org_id=$1 AND candidate_id=$2`, t.OrgID, candidateID)
		return err
	})
}

// DrainCandidateProjections is the process-restart recovery worker. Enumeration
// uses the existing narrow all_org_ids function; every job remains under tenant
// RLS. Multiple API processes safely serialize delivery at the org/site lock.
// At most 100 sites per org are delivered per pass. Successful sites are checked
// again after five minutes to repair late stale writes from legacy handlers.
func (s *Store) DrainCandidateProjections(ctx context.Context, w projection.Writer) (int, error) {
	orgs, err := s.ListAllOrgIDs(ctx)
	if err != nil {
		return 0, err
	}
	done := 0
	var failures []error
	if len(orgs) == 0 {
		return 0, nil
	}
	// Randomize scheduling so a slow tenant cannot deterministically starve
	// later tenants when the bounded worker deadline expires.
	rand.Shuffle(len(orgs), func(i, j int) { orgs[i], orgs[j] = orgs[j], orgs[i] })
	for _, org := range orgs {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		tenant := Tenant{OrgID: org, UserID: org}
		var ids []string
		err := s.withTxRaw(ctx, tenant, func(tx pgx.Tx, _ *db.Queries) error {
			rows, err := tx.Query(ctx, `SELECT candidate_id::text FROM app.candidate_projection_outbox WHERE org_id=$1 AND (delivered_at IS NULL OR delivered_at < now()-interval '5 minutes') ORDER BY delivered_at NULLS FIRST,created_at,candidate_id LIMIT 100`, org)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					return err
				}
				ids = append(ids, id)
			}
			return rows.Err()
		})
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, id := range ids {
			if err := s.FlushCandidateProjection(ctx, tenant, id, w); err != nil {
				failures = append(failures, err)
			} else {
				done++
			}
		}
	}
	return done, errors.Join(failures...)
}
