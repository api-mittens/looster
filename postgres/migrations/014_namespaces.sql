-- +goose Up
-- Bind every application function to its own schema. This also repairs historical
-- public-only paths when migrations are applied to a non-default namespace.
-- Functions must not inherit a direct SQL caller's search_path.
-- +goose StatementBegin
DO $$
DECLARE f record;
BEGIN
 FOR f IN SELECT p.proname,pg_get_function_identity_arguments(p.oid) AS args,n.nspname
  FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
  WHERE n.nspname=current_schema() AND left(p.proname,8)='looster_'
 LOOP
  EXECUTE format('ALTER FUNCTION %I.%I(%s) SET search_path TO %I, pg_temp',f.nspname,f.proname,f.args,f.nspname);
 END LOOP;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- Keep function paths pinned: rolling back namespace metadata must not weaken isolation.
SELECT 1;
