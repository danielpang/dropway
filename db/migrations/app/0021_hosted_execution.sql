-- +goose Up
CREATE TABLE app.provider_credentials (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), org_id uuid NOT NULL REFERENCES app.org_meta(id) ON DELETE CASCADE,
 owner_user_id uuid NOT NULL, provider text NOT NULL CHECK(provider IN ('anthropic','openrouter')),
 label text NOT NULL CHECK(char_length(label) BETWEEN 1 AND 80), last_four text NOT NULL,
 ciphertext bytea NOT NULL, generation integer NOT NULL DEFAULT 1,
 validated_at timestamptz, revoked_at timestamptz, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX provider_credentials_org_owner_idx ON app.provider_credentials(org_id,owner_user_id,id);
CREATE TABLE app.execution_jobs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), org_id uuid NOT NULL REFERENCES app.org_meta(id) ON DELETE CASCADE,
 room_id uuid NOT NULL REFERENCES app.project_rooms(id) ON DELETE CASCADE,
 site_id uuid NOT NULL REFERENCES app.sites(id) ON DELETE CASCADE,
 version_id uuid NOT NULL REFERENCES app.site_versions(id) ON DELETE CASCADE,
 content_hash text NOT NULL, initiator_user_id uuid NOT NULL,
 credential_id uuid NOT NULL REFERENCES app.provider_credentials(id), credential_generation integer NOT NULL,
 idempotency_key text NOT NULL, request_hash text NOT NULL, config jsonb NOT NULL,
 state text NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','running','clarification','completed','failed','cancelled')),
 active_attempt_id uuid, attempt_count integer NOT NULL DEFAULT 0, max_attempts integer NOT NULL DEFAULT 3,
 tokens_used bigint NOT NULL DEFAULT 0, calls_used integer NOT NULL DEFAULT 0,
 candidate_id uuid REFERENCES app.review_candidates(id) ON DELETE SET NULL, session_id uuid,
 failure_code text, created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(org_id,initiator_user_id,idempotency_key)
);
CREATE INDEX execution_jobs_org_queue_idx ON app.execution_jobs(org_id,state,created_at,id);
CREATE TABLE app.execution_attempts (
 id uuid PRIMARY KEY, org_id uuid NOT NULL REFERENCES app.org_meta(id) ON DELETE CASCADE,
 job_id uuid NOT NULL REFERENCES app.execution_jobs(id) ON DELETE CASCADE, worker_id text NOT NULL,
 token_hash text NOT NULL, gateway_hash text NOT NULL,
 state text NOT NULL CHECK(state IN ('running','completed','clarification','failed','expired','cancelled')),
 lease_expires_at timestamptz NOT NULL, deadline timestamptz NOT NULL,
 gateway_active boolean NOT NULL DEFAULT false, reserved_tokens bigint NOT NULL DEFAULT 0,
 submission_key text, submission_hash text,
 created_at timestamptz NOT NULL DEFAULT now(), finished_at timestamptz
);
CREATE INDEX execution_attempts_org_job_idx ON app.execution_attempts(org_id,job_id,id);
CREATE UNIQUE INDEX execution_attempts_one_active_idx ON app.execution_attempts(org_id,job_id) WHERE state='running';
CREATE TABLE app.execution_sessions (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),org_id uuid NOT NULL REFERENCES app.org_meta(id) ON DELETE CASCADE,
 job_id uuid NOT NULL REFERENCES app.execution_jobs(id) ON DELETE CASCADE,
 attempt_id uuid NOT NULL REFERENCES app.execution_attempts(id) ON DELETE CASCADE,
 question text NOT NULL,transcript jsonb NOT NULL,answer text,resume_key text,resumed_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX execution_sessions_org_job_idx ON app.execution_sessions(org_id,job_id,id);
CREATE TABLE app.execution_events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),org_id uuid NOT NULL REFERENCES app.org_meta(id) ON DELETE CASCADE,
 job_id uuid NOT NULL REFERENCES app.execution_jobs(id) ON DELETE CASCADE,
 attempt_id uuid REFERENCES app.execution_attempts(id) ON DELETE CASCADE,
 kind text NOT NULL,message text NOT NULL,created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX execution_events_org_job_idx ON app.execution_events(org_id,job_id,created_at,id);
-- +goose StatementBegin
DO $$ DECLARE name text; BEGIN
 FOREACH name IN ARRAY ARRAY['provider_credentials','execution_jobs','execution_attempts','execution_sessions','execution_events'] LOOP
  EXECUTE format('ALTER TABLE app.%I ENABLE ROW LEVEL SECURITY',name);
  EXECUTE format('ALTER TABLE app.%I FORCE ROW LEVEL SECURITY',name);
  EXECUTE format('CREATE POLICY tenant_isolation ON app.%I USING (org_id=NULLIF(current_setting(''app.current_org_id'',true),'''')::uuid) WITH CHECK (org_id=NULLIF(current_setting(''app.current_org_id'',true),'''')::uuid)',name);
  EXECUTE format('GRANT SELECT,INSERT,UPDATE,DELETE ON app.%I TO dropway_app',name);
 END LOOP;
END $$;
-- +goose StatementEnd
-- Cross-tenant foreign keys are validated independently of the request code.
-- This function owns only integrity checks; it never returns tenant data.
-- +goose StatementBegin
CREATE FUNCTION app.execution_integrity() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path TO app, pg_temp AS $$
BEGIN
 IF TG_TABLE_NAME='execution_jobs' THEN
  IF NOT EXISTS(SELECT 1 FROM app.project_rooms r JOIN app.sites s ON s.org_id=r.org_id
    JOIN app.site_versions v ON v.site_id=s.id AND v.org_id=s.org_id
    JOIN app.provider_credentials c ON c.org_id=r.org_id
    WHERE r.id=NEW.room_id AND r.org_id=NEW.org_id AND s.id=NEW.site_id
      AND v.id=NEW.version_id AND v.content_hash=NEW.content_hash
      AND c.id=NEW.credential_id AND c.owner_user_id=NEW.initiator_user_id) THEN
   RAISE EXCEPTION 'execution tenant binding mismatch' USING ERRCODE='23514';
  END IF;
  IF TG_OP='UPDATE' AND (NEW.org_id,NEW.room_id,NEW.site_id,NEW.version_id,NEW.content_hash,
    NEW.initiator_user_id,NEW.credential_id,NEW.credential_generation,NEW.config,NEW.idempotency_key,NEW.request_hash)
    IS DISTINCT FROM (OLD.org_id,OLD.room_id,OLD.site_id,OLD.version_id,OLD.content_hash,
    OLD.initiator_user_id,OLD.credential_id,OLD.credential_generation,OLD.config,OLD.idempotency_key,OLD.request_hash) THEN
   RAISE EXCEPTION 'execution configuration is immutable' USING ERRCODE='23514';
  END IF;
 ELSE
  IF NOT EXISTS(SELECT 1 FROM app.execution_jobs j WHERE j.id=NEW.job_id AND j.org_id=NEW.org_id) THEN
   RAISE EXCEPTION 'execution tenant binding mismatch' USING ERRCODE='23514';
  END IF;
  IF TG_TABLE_NAME IN ('execution_sessions','execution_events') THEN
   IF NEW.attempt_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM app.execution_attempts a WHERE a.id=NEW.attempt_id AND a.org_id=NEW.org_id AND a.job_id=NEW.job_id) THEN
   RAISE EXCEPTION 'execution attempt binding mismatch' USING ERRCODE='23514';
   END IF;
  END IF;
  IF TG_OP='UPDATE' AND (NEW.org_id,NEW.job_id) IS DISTINCT FROM (OLD.org_id,OLD.job_id) THEN
   RAISE EXCEPTION 'execution identity is immutable' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER execution_jobs_integrity BEFORE INSERT OR UPDATE ON app.execution_jobs FOR EACH ROW EXECUTE FUNCTION app.execution_integrity();
CREATE TRIGGER execution_attempts_integrity BEFORE INSERT OR UPDATE ON app.execution_attempts FOR EACH ROW EXECUTE FUNCTION app.execution_integrity();
CREATE TRIGGER execution_sessions_integrity BEFORE INSERT OR UPDATE ON app.execution_sessions FOR EACH ROW EXECUTE FUNCTION app.execution_integrity();
CREATE TRIGGER execution_events_integrity BEFORE INSERT OR UPDATE ON app.execution_events FOR EACH ROW EXECUTE FUNCTION app.execution_integrity();
REVOKE ALL ON FUNCTION app.execution_integrity() FROM PUBLIC;

-- +goose Down
DROP TABLE app.execution_events;
DROP TABLE app.execution_sessions;
DROP TABLE app.execution_attempts;
DROP TABLE app.execution_jobs;
DROP TABLE app.provider_credentials;

DROP FUNCTION app.execution_integrity();
