-- +goose Up
-- 单商品发布操作账本：记录客户端稳定操作 ID、请求指纹和远端创建阶段检查点。
CREATE TABLE single_publish_operations (
    operation_id TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL,
    cookie_id TEXT NOT NULL,
    request_fingerprint TEXT NOT NULL,
    state TEXT NOT NULL,
    item_id TEXT NOT NULL DEFAULT '',
    item_url TEXT NOT NULL DEFAULT '',
    raw_json TEXT NOT NULL DEFAULT '{}',
    error_message TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_single_publish_operations_user_created
    ON single_publish_operations(user_id, created_at DESC);
CREATE INDEX idx_single_publish_operations_cookie_state
    ON single_publish_operations(cookie_id, state);

-- +goose Down
DROP INDEX idx_single_publish_operations_cookie_state;
DROP INDEX idx_single_publish_operations_user_created;
DROP TABLE single_publish_operations;
