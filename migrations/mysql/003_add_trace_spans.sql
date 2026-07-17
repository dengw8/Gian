CREATE TABLE IF NOT EXISTS agent_trace_spans (
    run_id VARCHAR(64) NOT NULL,
    span_id VARCHAR(128) NOT NULL,
    revision INT NOT NULL,
    sequence INT NOT NULL,
    payload LONGTEXT NOT NULL,
    PRIMARY KEY (run_id, span_id),
    KEY idx_agent_trace_spans_run_sequence (run_id, sequence)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
