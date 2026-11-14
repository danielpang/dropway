-- +goose Up
-- A mention and its job commit together; retries return the same reply/job pair.
ALTER TABLE app.execution_jobs ADD COLUMN trigger_reply_id uuid REFERENCES app.review_replies(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX execution_jobs_trigger_reply_idx ON app.execution_jobs(trigger_reply_id) WHERE trigger_reply_id IS NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION app.mention_execution_binding() RETURNS trigger
LANGUAGE plpgsql SET search_path TO app, pg_temp AS $$
BEGIN
 IF NEW.trigger_reply_id IS NOT NULL AND NOT EXISTS (
  SELECT 1 FROM app.review_replies r JOIN app.review_threads t ON t.id=r.thread_id AND t.org_id=r.org_id
  WHERE r.id=NEW.trigger_reply_id AND r.org_id=NEW.org_id AND r.author_user_id=NEW.initiator_user_id
   AND t.room_id=NEW.room_id AND t.site_id=NEW.site_id AND t.version_id=NEW.version_id
   AND t.id::text=NEW.config->>'thread_id'
 ) THEN RAISE EXCEPTION 'mention execution binding mismatch' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER mention_execution_binding BEFORE INSERT OR UPDATE OF trigger_reply_id ON app.execution_jobs
FOR EACH ROW EXECUTE FUNCTION app.mention_execution_binding();

-- +goose Down
DROP TRIGGER mention_execution_binding ON app.execution_jobs;
DROP FUNCTION app.mention_execution_binding();
ALTER TABLE app.execution_jobs DROP COLUMN trigger_reply_id;
