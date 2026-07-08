-- CASE guard: jsonb_array_elements raises on non-array input. Legacy
-- content_version=0 rows store scalar JSON strings; excluded from search
-- by design. IMMUTABLE for expression index use.
CREATE FUNCTION chat_message_search_text(content jsonb) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE WHEN jsonb_typeof(content) = 'array' THEN (
        SELECT string_agg(part->>'text', ' ' ORDER BY ordinality)
        FROM jsonb_array_elements(content) WITH ORDINALITY AS t(part, ordinality)
        WHERE part->>'type' = 'text'
    ) END
$$;

-- Populated by a background sweep, not at insert time. NULL means pending.
ALTER TABLE chat_messages ADD COLUMN search_tsv tsvector;

CREATE INDEX idx_chat_messages_search_tsv ON chat_messages
USING GIN (search_tsv)
WHERE ((search_tsv IS NOT NULL) AND (deleted = false) AND (visibility = ANY (ARRAY['user'::chat_message_visibility, 'both'::chat_message_visibility])) AND (role = ANY (ARRAY['user'::chat_message_role, 'assistant'::chat_message_role])));

-- Lets the sweep find pending rows without a full table scan. Queries must
-- repeat the full predicate to use this index.
CREATE INDEX idx_chat_messages_search_tsv_pending ON chat_messages USING btree (id DESC)
WHERE ((search_tsv IS NULL) AND (deleted = false) AND (visibility = ANY (ARRAY['user'::chat_message_visibility, 'both'::chat_message_visibility])) AND (role = ANY (ARRAY['user'::chat_message_role, 'assistant'::chat_message_role])));

CREATE INDEX idx_chats_title_fts ON chats
    USING GIN (to_tsvector('simple', title));

CREATE INDEX idx_chat_diff_statuses_pr_title_fts ON chat_diff_statuses
    USING GIN (to_tsvector('simple', pull_request_title));
