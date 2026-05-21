package email

import "context"

// Mailer sends HTML email.
type Mailer interface {
	SendHTML(ctx context.Context, to []string, subject, htmlBody string) error
}
