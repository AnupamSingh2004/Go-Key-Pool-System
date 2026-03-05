-- Create requests table to store all API requests
CREATE TABLE IF NOT EXISTS requests (
    id TEXT PRIMARY KEY,
    idempotency_key TEXT UNIQUE,
    source TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 2,
    method TEXT NOT NULL DEFAULT 'POST',
    destination_url TEXT NOT NULL,
    headers TEXT,
    payload TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    assigned_key_id TEXT,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    response_status INTEGER,
    response_body TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT (datetime('now')),
    updated_at TIMESTAMP NOT NULL DEFAULT (datetime('now')),
    completed_at TIMESTAMP
);

-- Create indexes for efficient querying
CREATE INDEX IF NOT EXISTS idx_requests_status ON requests(status);
CREATE INDEX IF NOT EXISTS idx_requests_idempotency ON requests(idempotency_key);
CREATE INDEX IF NOT EXISTS idx_requests_created ON requests(created_at);
