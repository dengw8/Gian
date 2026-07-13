CREATE TABLE IF NOT EXISTS agent_runs (
    id VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    model_provider VARCHAR(128) NOT NULL,
    error_message LONGTEXT NOT NULL,
    plan_mode VARCHAR(32) NOT NULL,
    plan_steps INT NOT NULL,
    plan_completed INT NOT NULL,
    prompt_tokens BIGINT NULL,
    completion_tokens BIGINT NULL,
    total_tokens BIGINT NULL,
    cached_tokens BIGINT NULL,
    reasoning_tokens BIGINT NULL,
    token_usage_raw LONGTEXT NULL,
    PRIMARY KEY (id),
    KEY idx_agent_runs_updated (updated_at, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS agent_run_messages (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    run_id VARCHAR(64) NOT NULL,
    payload LONGTEXT NOT NULL,
    PRIMARY KEY (id),
    KEY idx_agent_run_messages_run_id (run_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS agent_run_events (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    run_id VARCHAR(64) NOT NULL,
    payload LONGTEXT NOT NULL,
    PRIMARY KEY (id),
    KEY idx_agent_run_events_run_id (run_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS agent_model_logs (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    run_id VARCHAR(64) NOT NULL,
    payload LONGTEXT NOT NULL,
    PRIMARY KEY (id),
    KEY idx_agent_model_logs_run_id (run_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS agent_run_states (
    run_id VARCHAR(64) NOT NULL,
    plan_payload LONGTEXT NULL,
    compaction_payload LONGTEXT NULL,
    PRIMARY KEY (run_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
