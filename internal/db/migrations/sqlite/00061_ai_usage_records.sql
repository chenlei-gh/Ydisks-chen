-- +goose Up
-- AI 用量账本只保存模型提供商、模型名、任务类型、Token 和耗时，不保存提示词、响应正文或 API Key。
CREATE TABLE ai_usage_records (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    cookie_id TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL,
    task_type TEXT NOT NULL,
    prompt_tokens BIGINT NOT NULL DEFAULT 0,
    completion_tokens BIGINT NOT NULL DEFAULT 0,
    total_tokens BIGINT NOT NULL DEFAULT 0,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    error_kind TEXT NOT NULL DEFAULT '',
    usage_date TEXT NOT NULL,
    created_at_unix BIGINT NOT NULL
);
CREATE INDEX idx_ai_usage_cookie_date ON ai_usage_records(cookie_id, usage_date);
CREATE INDEX idx_ai_usage_date ON ai_usage_records(usage_date);

-- +goose Down
DROP INDEX idx_ai_usage_date;
DROP INDEX idx_ai_usage_cookie_date;
DROP TABLE ai_usage_records;
