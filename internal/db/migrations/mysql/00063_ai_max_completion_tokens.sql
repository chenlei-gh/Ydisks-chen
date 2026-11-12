-- +goose Up
ALTER TABLE ai_reply_settings ADD COLUMN max_completion_tokens INT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE ai_reply_settings DROP COLUMN max_completion_tokens;
