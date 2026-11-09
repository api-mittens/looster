-- +goose Up
-- +goose StatementBegin
-- Prefer locale-independent Unicode casing where supported. Existing SQL_ASCII
-- and non-ICU installations must still upgrade: C supplies exact ASCII casing,
-- with intentionally limited non-ASCII parity on those installations.
DO $$
BEGIN
 IF current_setting('server_encoding')='UTF8'
    AND EXISTS (SELECT FROM pg_collation WHERE collprovider='i') THEN
  CREATE COLLATION palette_unicode (provider = icu, locale = 'und');
 ELSE
  CREATE COLLATION palette_unicode FROM "C";
 END IF;
END
$$;

-- Mirror static/search.mjs. UTF8 positions and length use UTF-16 code units,
-- not PostgreSQL character counts. Other encodings retain native positions.
CREATE FUNCTION palette_utf16_length(value text) RETURNS integer
LANGUAGE plpgsql IMMUTABLE STRICT PARALLEL SAFE SET search_path=public,pg_temp AS $$
BEGIN
 IF current_setting('server_encoding')='UTF8' THEN
  RETURN length(value) + length(regexp_replace(value COLLATE "C", '[^'||chr(65536)||'-'||chr(1114111)||']', '', 'g'));
 END IF;
 RETURN length(value);
END
$$;

CREATE FUNCTION palette_fuzzy_score(query text, value text) RETURNS double precision
LANGUAGE plpgsql IMMUTABLE STRICT PARALLEL SAFE SET search_path=public,pg_temp AS $$
DECLARE
 whitespace text := E' \t\n\r\f' || chr(11);
 needle text;
 hay text := lower(value COLLATE palette_unicode);
 character text;
 at_pos integer := 1;
 found integer;
 units integer;
 previous integer := -2;
 score integer := 0;
 boundaries text;
BEGIN
 -- Do not evaluate Unicode chr() on SQL_ASCII/legacy database encodings.
 IF current_setting('server_encoding')='UTF8' THEN
  whitespace := whitespace || chr(160) || chr(5760) || chr(8192) || chr(8193) || chr(8194) || chr(8195) || chr(8196) || chr(8197) || chr(8198) || chr(8199) || chr(8200) || chr(8201) || chr(8202) || chr(8232) || chr(8233) || chr(8239) || chr(8287) || chr(12288) || chr(65279);
 END IF;
 needle := lower(btrim(query,whitespace) COLLATE palette_unicode);
 boundaries := ' /._:#-' || whitespace;
 IF needle = '' THEN RETURN 0; END IF;
 FOR i IN 1..length(needle) LOOP
  character := substr(needle,i,1);
  found := strpos(substr(hay,at_pos),character);
  IF found = 0 THEN RETURN -1; END IF;
  found := found + at_pos - 1;
  units := palette_utf16_length(substr(hay,1,found-1));
  score := score + 1 + CASE WHEN units=previous+1 THEN 3 ELSE 0 END
   + CASE WHEN found=1 OR strpos(boundaries,substr(hay,found-1,1))>0 THEN 4 ELSE 0 END;
  previous := units;
  at_pos := found+1;
 END LOOP;
 RETURN score - palette_utf16_length(hay)::double precision / 100;
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION palette_fuzzy_score(text,text);
DROP FUNCTION palette_utf16_length(text);
DROP COLLATION palette_unicode;
