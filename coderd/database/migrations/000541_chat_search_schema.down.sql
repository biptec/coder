DROP INDEX IF EXISTS idx_chat_diff_statuses_pr_title_fts;

DROP INDEX IF EXISTS idx_chats_title_fts;

DROP INDEX IF EXISTS idx_chat_messages_search_tsv_pending;

DROP INDEX IF EXISTS idx_chat_messages_search_tsv;

ALTER TABLE chat_messages DROP COLUMN IF EXISTS search_tsv;

DROP FUNCTION IF EXISTS chat_message_search_text(jsonb);
