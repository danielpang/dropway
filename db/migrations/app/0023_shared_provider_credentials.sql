-- +goose Up
ALTER TABLE app.provider_credentials ADD COLUMN scope text NOT NULL DEFAULT 'personal' CHECK(scope IN ('personal','team'));
CREATE INDEX provider_credentials_org_scope_idx ON app.provider_credentials(org_id,scope,created_at,id);
ALTER TABLE app.provider_credentials DROP CONSTRAINT provider_credentials_provider_check;
ALTER TABLE app.provider_credentials ADD CONSTRAINT provider_credentials_provider_check CHECK(provider IN ('anthropic','openrouter','openai'));
-- Scope, creator and provider are immutable encryption/authorization bindings.
-- +goose StatementBegin
CREATE FUNCTION app.provider_credential_binding() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.org_id,NEW.owner_user_id,NEW.scope,NEW.provider) IS DISTINCT FROM (OLD.org_id,OLD.owner_user_id,OLD.scope,OLD.provider) THEN
  RAISE EXCEPTION 'credential identity is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER provider_credential_binding BEFORE UPDATE ON app.provider_credentials FOR EACH ROW EXECUTE FUNCTION app.provider_credential_binding();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app.execution_integrity() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path TO app, pg_temp AS $$
BEGIN
 IF TG_TABLE_NAME='execution_jobs' THEN
  IF NOT EXISTS(SELECT 1 FROM app.project_rooms r JOIN app.sites s ON s.org_id=r.org_id
    JOIN app.site_versions v ON v.site_id=s.id AND v.org_id=s.org_id
    JOIN app.provider_credentials c ON c.org_id=r.org_id
    WHERE r.id=NEW.room_id AND r.org_id=NEW.org_id AND s.id=NEW.site_id
      AND v.id=NEW.version_id AND v.content_hash=NEW.content_hash
      AND c.id=NEW.credential_id AND (c.owner_user_id=NEW.initiator_user_id OR c.scope='team')) THEN
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

-- +goose Down
-- Refuse destructive downgrade while new credential types are present.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM app.provider_credentials WHERE scope='team' OR provider='openai') THEN
  RAISE EXCEPTION 'remove shared/OpenAI credentials and their execution history before downgrade';
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app.execution_integrity() RETURNS trigger
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
DROP TRIGGER provider_credential_binding ON app.provider_credentials;
DROP FUNCTION app.provider_credential_binding();
ALTER TABLE app.provider_credentials DROP CONSTRAINT provider_credentials_provider_check;
ALTER TABLE app.provider_credentials ADD CONSTRAINT provider_credentials_provider_check CHECK(provider IN ('anthropic','openrouter'));
DROP INDEX app.provider_credentials_org_scope_idx;
ALTER TABLE app.provider_credentials DROP COLUMN scope;
