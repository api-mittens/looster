-- +goose Up
-- The stop cause is fixed control metadata, not an external error payload.
ALTER TABLE jobs ADD COLUMN stop_reason text NOT NULL DEFAULT ''
 CHECK (stop_reason IN ('','ping stopped by behaviour'));

-- Keep the dispatch function and its namespace search path unchanged except
-- for the stop cause field. Do not remove the payload fence.
-- +goose StatementBegin
DO $$
DECLARE definition text;
BEGIN
 SELECT pg_get_functiondef('looster_dispatch()'::regprocedure) INTO definition;
 IF position('state=''failed'',error=''ping stopped by behaviour''' IN definition)=0 THEN
  RAISE EXCEPTION 'Cannot find the dispatch stop assignment';
 END IF;
 EXECUTE replace(definition,
  'state=''failed'',error=''ping stopped by behaviour''',
  'state=''failed'',stop_reason=''ping stopped by behaviour''');
END; $$;
-- +goose StatementEnd

-- +goose Down
-- Do not remove stored stop causes or restore an incompatible active writer.
-- +goose StatementBegin
DO $$
DECLARE definition text;
BEGIN
 IF EXISTS(SELECT FROM jobs WHERE stop_reason<>'')
  OR EXISTS(SELECT FROM payload_cutover WHERE enabled) THEN
  RAISE EXCEPTION 'Cannot remove stop cause metadata with stored causes or active payload cutover';
 END IF;
 SELECT pg_get_functiondef('looster_dispatch()'::regprocedure) INTO definition;
 EXECUTE replace(definition,
  'state=''failed'',stop_reason=''ping stopped by behaviour''',
  'state=''failed'',error=''ping stopped by behaviour''');
END; $$;
-- +goose StatementEnd
ALTER TABLE jobs DROP COLUMN stop_reason;
