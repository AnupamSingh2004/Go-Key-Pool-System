-- Seed default system configuration values
INSERT OR IGNORE INTO system_config (key, value, description) VALUES
    ('worker_count', '10', 'Number of concurrent worker goroutines'),
    ('queue_max_size', '1000', 'Maximum pending requests in queue'),
    ('strategy', 'round_robin', 'Key selection strategy: round_robin or weighted_round_robin'),
    ('circuit_breaker_threshold', '3', 'Failures before circuit opens'),
    ('circuit_breaker_open_duration_seconds', '60', 'Seconds before half-open probe'),
    ('retry_max_attempts', '4', 'Max total attempts per request'),
    ('retry_base_delay_ms', '1000', 'Base delay ms for exponential backoff'),
    ('retry_max_delay_ms', '30000', 'Maximum retry delay ms'),
    ('load_shed_level1', '0.50', 'Healthy key ratio below which Low priority is rejected'),
    ('load_shed_level2', '0.25', 'Healthy key ratio below which Normal priority is also rejected');
