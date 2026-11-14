// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"errors"

	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// ErrControlledSite means a non-human caller tried to mutate a site that is
// in an enabled project room. The check runs while the org and site rows are locked.
var ErrControlledSite = errors.New("store: controlled site requires a human session")

type humanSessionKey struct{}

// WithHumanSession records whether this request was classified as an
// interactive human session. A missing value is not a human session.
func WithHumanSession(ctx context.Context, human bool) context.Context {
	return context.WithValue(ctx, humanSessionKey{}, human)
}

// HumanSessionFromContext reports the classification stored by WithHumanSession.
func HumanSessionFromContext(ctx context.Context) bool {
	human, _ := ctx.Value(humanSessionKey{}).(bool)
	return human
}

// lockOrgThenSite locks the org flag row and then the site. Callers that add
// or remove project-room protection use the same order.
func lockOrgThenSite(ctx context.Context, q *db.Queries, orgID, siteID string) (db.AppSite, error) {
	if _, err := q.LockOrgMeta(ctx, orgID); err != nil {
		if isNoRows(err) {
			return db.AppSite{}, ErrNotFound
		}
		return db.AppSite{}, err
	}
	locked, err := q.LockSite(ctx, db.LockSiteParams{ID: siteID, OrgID: orgID})
	if err != nil {
		if isNoRows(err) {
			return db.AppSite{}, ErrNotFound
		}
		return db.AppSite{}, err
	}
	return locked, nil
}

// rejectNonHumanControlledSite fails closed when the locked site is in an
// enabled project room and the caller is not a human session.
func rejectNonHumanControlledSite(ctx context.Context, q *db.Queries, orgID, siteID string) error {
	controlled, err := q.SiteInEnabledProjectRoom(ctx, db.SiteInEnabledProjectRoomParams{OrgID: orgID, SiteID: siteID})
	if err != nil {
		return err
	}
	if controlled && !HumanSessionFromContext(ctx) {
		return ErrControlledSite
	}
	return nil
}
