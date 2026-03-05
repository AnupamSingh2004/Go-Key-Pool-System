-- Create key_events table to track API key lifecycle events
CREATE TABLE IF NOT EXISTS key_events (
    id TEXT PRIMARY KEY,
    key_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    message TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT (datetime('now'))
);

-- Create index for efficient key event lookup
CREATE INDEX IF NOT EXISTS idx_key_events_key_id ON key_events(key_id);
