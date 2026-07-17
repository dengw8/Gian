CREATE TABLE IF NOT EXISTS agent_execution_events (
    run_id VARCHAR(64) NOT NULL,
    sequence BIGINT NOT NULL,
    payload LONGTEXT NOT NULL,
    PRIMARY KEY (run_id, sequence),
    KEY idx_agent_execution_events_run_sequence (run_id, sequence)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
