-- +goose Up
-- AI 用量账本不保存提示词、响应正文或 API Key。
CREATE TABLE ai_usage_records (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    cookie_id VARCHAR(128) NOT NULL,
    provider VARCHAR(255) NOT NULL DEFAULT '',
    model VARCHAR(255) NOT NULL,
    task_type VARCHAR(16) NOT NULL,
    prompt_tokens BIGINT NOT NULL DEFAULT 0,
    completion_tokens BIGINT NOT NULL DEFAULT 0,
    total_tokens BIGINT NOT NULL DEFAULT 0,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    status VARCHAR(16) NOT NULL,
    error_kind VARCHAR(64) NOT NULL DEFAULT '',
    usage_date CHAR(10) NOT NULL,
    created_at_unix BIGINT NOT NULL,
    INDEX idx_ai_usage_cookie_date (cookie_id, usage_date),
    INDEX idx_ai_usage_date (usage_date),
    CONSTRAINT fk_ai_usage_cookie FOREIGN KEY (cookie_id) REFERENCES cookies(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose Down
DROP TABLE ai_usage_records;
