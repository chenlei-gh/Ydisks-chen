-- +goose Up
ALTER TABLE chat_sessions
    ADD COLUMN auto_reply_mode VARCHAR(16) NOT NULL DEFAULT 'auto'
        CHECK (auto_reply_mode IN ('auto', 'manual')),
    ADD COLUMN reply_gate_version BIGINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE chat_sessions DROP COLUMN reply_gate_version;
ALTER TABLE chat_sessions DROP COLUMN auto_reply_mode;
