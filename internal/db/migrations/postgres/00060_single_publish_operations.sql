-- +goose Up
-- 单商品发布操作账本：记录客户端稳定操作 ID、请求指纹和远端创建阶段检查点。
CREATE TABLE single_publish_operations (
    operation_id VARCHAR(64) PRIMARY KEY,
    user_id BIGINT NOT NULL,
    cookie_id VARCHAR(128) NOT NULL,
    request_fingerprint CHAR(64) NOT NULL,
    state VARCHAR(32) NOT NULL,
    item_id VARCHAR(128) NOT NULL DEFAULT '',
    item_url TEXT NOT NULL DEFAULT '',
    raw_json TEXT NOT NULL DEFAULT '{}',
    error_message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_single_publish_operations_user_created
    ON single_publish_operations(user_id, created_at DESC);
CREATE INDEX idx_single_publish_operations_cookie_state
    ON single_publish_operations(cookie_id, state);

-- +goose Down
DROP INDEX idx_single_publish_operations_cookie_state;
DROP INDEX idx_single_publish_operations_user_created;
DROP TABLE single_publish_operations;
