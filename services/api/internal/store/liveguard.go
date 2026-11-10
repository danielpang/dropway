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

// ErrCandidatePublishDenied means a live publish would apply candidate bytes
// through the legacy route instead of the verified, receipted apply path.
var ErrCandidatePublishDenied = errors.New("store: candidate publish denied")

// ErrCandidateArtifact means the stored bytes do not match the candidate's pinned hashes.
var ErrCandidateArtifact = errors.New("store: candidate artifact mismatch")

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

// lockOrgThenSite holds a shared org flag lock, then an exclusive site lock.
// Unrelated site mutations can proceed concurrently; feature-flag updates wait
// for these readers, and attachment changes serialize on the same site lock.
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
// guardControlledSite locks the org, then the site, and rejects a non-human
// mutation when that site is in an enabled project room.
func guardControlledSite(ctx context.Context, q *db.Queries, orgID, siteID string) error {
	if siteID == "" {
		return nil
	}
	if _, err := lockOrgThenSite(ctx, q, orgID, siteID); err != nil {
		return err
	}
	return rejectNonHumanControlledSite(ctx, q, orgID, siteID)
}

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
