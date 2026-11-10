-- +goose Up
-- Due-work discovery avoids visiting every tenant when rooms are unused.
CREATE INDEX candidate_projection_outbox_due_idx
    ON app.candidate_projection_outbox(delivered_at NULLS FIRST, org_id);

-- +goose StatementBegin
CREATE FUNCTION app.candidate_projection_org_ids() RETURNS TABLE(id uuid)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path TO app, pg_temp AS $$
BEGIN
    IF current_setting('app.ops_mode', true) IS DISTINCT FROM '1' THEN
        RAISE EXCEPTION 'candidate projection enumeration is ops-only'
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN QUERY SELECT DISTINCT o.org_id
      FROM app.candidate_projection_outbox o
      WHERE o.delivered_at IS NULL OR o.delivered_at < now() - interval '5 minutes';
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.candidate_projection_org_ids() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.candidate_projection_org_ids() TO dropway_app;

-- +goose Down
DROP FUNCTION app.candidate_projection_org_ids();
DROP INDEX app.candidate_projection_outbox_due_idx;
