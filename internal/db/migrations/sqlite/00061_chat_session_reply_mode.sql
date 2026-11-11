-- +goose Up
ALTER TABLE chat_sessions ADD COLUMN auto_reply_mode TEXT NOT NULL DEFAULT 'auto'
    CHECK (auto_reply_mode IN ('auto', 'manual'));
ALTER TABLE chat_sessions ADD COLUMN reply_gate_version INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE chat_sessions DROP COLUMN reply_gate_version;
ALTER TABLE chat_sessions DROP COLUMN auto_reply_mode;
