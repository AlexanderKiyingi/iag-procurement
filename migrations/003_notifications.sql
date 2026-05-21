-- In-app notifications feed + durable audit of outbound email jobs

CREATE TABLE IF NOT EXISTS notifications (
    id BIGSERIAL PRIMARY KEY,
    event_type TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    severity TEXT NOT NULL DEFAULT 'info',
    read_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_notifications_created ON notifications (created_at DESC);

CREATE INDEX IF NOT EXISTS idx_notifications_unread ON notifications (id) WHERE read_at IS NULL;

CREATE TABLE IF NOT EXISTS notification_email_jobs (
    id BIGSERIAL PRIMARY KEY,
    template TEXT NOT NULL,
    subject TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'queued',
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sent_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_notification_email_jobs_status ON notification_email_jobs (status, created_at);
