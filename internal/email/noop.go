package email

import "context"

// NoopMailer drops messages (SMTP not configured).
type NoopMailer struct{}

func (NoopMailer) SendHTML(_ context.Context, _ []string, _, _ string) error {
	return nil
}
