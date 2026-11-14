-- +goose Up
-- A receipt is separate from the terminal human approval decision. It is written
-- in the same transaction as the live pointer, preventing approval replay.
CREATE TABLE app.candidate_applications (
    candidate_id uuid PRIMARY KEY REFERENCES app.review_candidates(id) ON DELETE CASCADE,
    org_id uuid NOT NULL REFERENCES app.org_meta(id) ON DELETE CASCADE,
    -- Historical identity, validated at insert; no FK that can block site deletion.
    version_id uuid NOT NULL,
    applied_by uuid NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX candidate_applications_org_idx ON app.candidate_applications(org_id, candidate_id);
ALTER TABLE app.candidate_applications ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.candidate_applications FORCE ROW LEVEL SECURITY;
CREATE POLICY candidate_applications_tenant_isolation ON app.candidate_applications
    USING (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid)
    WITH CHECK (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid);
GRANT SELECT, INSERT ON app.candidate_applications TO dropway_app;

-- +goose StatementBegin
CREATE FUNCTION app.validate_candidate_application() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path TO app, pg_temp AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM app.review_candidates c JOIN app.site_versions v
          ON v.id = NEW.version_id AND v.org_id = c.org_id AND v.site_id = c.site_id
        WHERE c.id = NEW.candidate_id AND c.org_id = NEW.org_id
          AND c.state = 'approved' AND v.content_hash = c.content_hash
    ) THEN
        RAISE EXCEPTION 'candidate application must match an approved artifact' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER candidate_application_identity BEFORE INSERT ON app.candidate_applications
    FOR EACH ROW EXECUTE FUNCTION app.validate_candidate_application();
REVOKE ALL ON FUNCTION app.validate_candidate_application() FROM PUBLIC;

ALTER TABLE app.review_activity DROP CONSTRAINT review_activity_kind_check;
ALTER TABLE app.review_activity ADD CONSTRAINT review_activity_kind_check CHECK (kind IN (
 'room_created', 'member_added', 'member_removed', 'member_role_changed',
 'ownership_transferred', 'site_added', 'site_removed',
 'thread_created', 'reply_created', 'thread_resolved', 'thread_reopened',
 'anchor_reanchored', 'anchor_orphaned', 'version_signoff', 'version_signoff_withdrawn',
 'candidate_submitted', 'candidate_approved', 'candidate_rejected', 'candidate_conflict', 'candidate_applied'
));

-- Projection intent commits with the live pointer. Delivery is retryable and
-- reconstructs current routes rather than replaying an obsolete access policy.
CREATE TABLE app.candidate_projection_outbox (
    candidate_id uuid PRIMARY KEY REFERENCES app.candidate_applications(candidate_id) ON DELETE CASCADE,
    org_id uuid NOT NULL REFERENCES app.org_meta(id) ON DELETE CASCADE,
    site_id uuid NOT NULL REFERENCES app.sites(id) ON DELETE CASCADE,
    deleted_preview_hosts jsonb NOT NULL DEFAULT '[]'::jsonb,
    delivered_at timestamptz,
    UNIQUE (org_id, site_id),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX candidate_projection_outbox_org_idx ON app.candidate_projection_outbox(org_id, delivered_at NULLS FIRST, created_at, candidate_id);
ALTER TABLE app.candidate_projection_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.candidate_projection_outbox FORCE ROW LEVEL SECURITY;
CREATE POLICY candidate_projection_outbox_tenant_isolation ON app.candidate_projection_outbox
    USING (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid)
    WITH CHECK (org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON app.candidate_projection_outbox TO dropway_app;

-- +goose Down
DROP TABLE app.candidate_projection_outbox;
DELETE FROM app.review_activity WHERE kind='candidate_applied';
ALTER TABLE app.review_activity DROP CONSTRAINT review_activity_kind_check;
ALTER TABLE app.review_activity ADD CONSTRAINT review_activity_kind_check CHECK (kind IN (
 'room_created', 'member_added', 'member_removed', 'member_role_changed',
 'ownership_transferred', 'site_added', 'site_removed',
 'thread_created', 'reply_created', 'thread_resolved', 'thread_reopened',
 'anchor_reanchored', 'anchor_orphaned', 'version_signoff', 'version_signoff_withdrawn',
 'candidate_submitted', 'candidate_approved', 'candidate_rejected', 'candidate_conflict'
));

DROP TABLE app.candidate_applications;
DROP FUNCTION app.validate_candidate_application();
