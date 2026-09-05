package testutil

import (
	"context"
	"regexp"
	"sync"

	"tms/backend/internal/notify"
)

// otpCodeRe pulls the 6-digit code out of the dev SMS body, exactly as an
// operator would read it out of .dev/api.log.
var otpCodeRe = regexp.MustCompile(`\b(\d{6})\b`)

// SMSCapture is a notify.SMSProvider that records what the LogProvider would
// have logged, so tests can read an OTP the same way dev does.
type SMSCapture struct {
	mu       sync.Mutex
	inner    notify.SMSProvider
	messages []SMSMessage
}

// SMSMessage is one captured SMS.
type SMSMessage struct {
	To     string
	Body   string
	Sender string
}

// NewSMSCapture wraps a LogProvider so messages are both logged and captured.
func NewSMSCapture() *SMSCapture {
	return &SMSCapture{inner: notify.NewLogProvider(Logger())}
}

// Send records the message and forwards it to the wrapped provider.
func (c *SMSCapture) Send(ctx context.Context, to, body, senderName string) (string, error) {
	c.mu.Lock()
	c.messages = append(c.messages, SMSMessage{To: to, Body: body, Sender: senderName})
	c.mu.Unlock()
	return c.inner.Send(ctx, to, body, senderName)
}

// Messages returns a copy of everything captured so far.
func (c *SMSCapture) Messages() []SMSMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]SMSMessage, len(c.messages))
	copy(out, c.messages)
	return out
}

// LastOTP returns the most recent 6-digit code sent to a phone number.
func (c *SMSCapture) LastOTP(phone string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.messages) - 1; i >= 0; i-- {
		if c.messages[i].To != phone {
			continue
		}
		if m := otpCodeRe.FindStringSubmatch(c.messages[i].Body); m != nil {
			return m[1]
		}
	}
	return ""
}

// EmailCapture is a notify.EmailProvider recording verification and invite
// links (dev logs them as `email_link`).
type EmailCapture struct {
	mu     sync.Mutex
	inner  notify.EmailProvider
	emails []EmailMessage
}

// EmailMessage is one captured email.
type EmailMessage struct {
	To      string
	Subject string
	Link    string
	Body    string
}

// NewEmailCapture wraps a LogEmailProvider.
func NewEmailCapture() *EmailCapture {
	return &EmailCapture{inner: notify.NewLogEmailProvider(Logger())}
}

// Send records the email and forwards it to the wrapped provider.
func (c *EmailCapture) Send(ctx context.Context, to, subject, link, body string) (string, error) {
	c.mu.Lock()
	c.emails = append(c.emails, EmailMessage{To: to, Subject: subject, Link: link, Body: body})
	c.mu.Unlock()
	return c.inner.Send(ctx, to, subject, link, body)
}

// LastLink returns the most recent link mailed to an address.
func (c *EmailCapture) LastLink(to string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.emails) - 1; i >= 0; i-- {
		if c.emails[i].To == to {
			return c.emails[i].Link
		}
	}
	return ""
}

// Count returns how many emails were sent.
func (c *EmailCapture) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.emails)
}
