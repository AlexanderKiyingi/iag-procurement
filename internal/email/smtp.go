package email

import (
	"bytes"
	"context"
	"fmt"
	"net/smtp"
	"strings"
)

// SMTPMailer sends RFC 5322 messages with an HTML part via plain SMTP (STARTTLS not handled here;
// use port 465 with implicit TLS in front of a relay, or a provider that accepts plain 587 in dev).
type SMTPMailer struct {
	Host, User, Password, From string
	Port int
}

func (m *SMTPMailer) SendHTML(ctx context.Context, to []string, subject, htmlBody string) error {
	if len(to) == 0 {
		return fmt.Errorf("email: no recipients")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	boundary := "iag-proc--boundary"
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", m.From)
	fmt.Fprintf(&buf, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: multipart/alternative; boundary=%s\r\n\r\n", boundary)
	fmt.Fprintf(&buf, "--%s\r\n", boundary)
	fmt.Fprintf(&buf, "Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	fmt.Fprintf(&buf, "This message requires an HTML-capable client.\r\n")
	fmt.Fprintf(&buf, "--%s\r\n", boundary)
	fmt.Fprintf(&buf, "Content-Type: text/html; charset=UTF-8\r\n\r\n")
	buf.WriteString(htmlBody)
	fmt.Fprintf(&buf, "\r\n--%s--\r\n", boundary)

	addr := fmt.Sprintf("%s:%d", m.Host, m.Port)
	auth := smtp.PlainAuth("", m.User, m.Password, m.Host)
	return smtp.SendMail(addr, auth, m.From, to, buf.Bytes())
}
