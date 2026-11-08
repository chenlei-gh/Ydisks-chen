-- +goose Up
-- 单商品发布操作账本：记录客户端稳定操作 ID、请求指纹和远端创建阶段检查点。
CREATE TABLE single_publish_operations (
    operation_id VARCHAR(64) PRIMARY KEY,
    user_id BIGINT NOT NULL,
    cookie_id VARCHAR(128) NOT NULL,
    request_fingerprint CHAR(64) NOT NULL,
    state VARCHAR(32) NOT NULL,
    item_id VARCHAR(128) NOT NULL DEFAULT '',
    item_url TEXT NOT NULL,
    raw_json LONGTEXT NOT NULL,
    error_message TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_single_publish_operations_user_created (user_id, created_at),
    INDEX idx_single_publish_operations_cookie_state (cookie_id, state)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose Down
DROP TABLE single_publish_operations;
