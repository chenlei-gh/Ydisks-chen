-- +goose Up
ALTER TABLE chat_sessions
    ADD COLUMN auto_reply_mode VARCHAR(16) NOT NULL DEFAULT 'auto',
    ADD COLUMN reply_gate_version BIGINT NOT NULL DEFAULT 0,
    ADD CONSTRAINT chk_chat_sessions_auto_reply_mode CHECK (auto_reply_mode IN ('auto', 'manual'));

-- +goose Down
ALTER TABLE chat_sessions
    DROP CHECK chk_chat_sessions_auto_reply_mode,
    DROP COLUMN reply_gate_version,
    DROP COLUMN auto_reply_mode;
