package notify

import (
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

// emailTarget is a parsed mailto:// / mailtos:// destination.
type emailTarget struct {
	host, port         string
	username, password string
	from, fromName     string
	to                 []string
	implicitTLS        bool // mailtos:// → TLS on connect (typically port 465)
}

// sendEmail parses a mailto(s):// URL and delivers the message over SMTP.
//
// Format: mailto://user:pass@smtp.host[:port]/?to=you@example.com[&from=…][&name=…]
// mailtos:// uses implicit TLS (default port 465); mailto:// uses STARTTLS when the
// server offers it (default port 587). This is a faithful common-case subset of the
// Apprise mailto convention — explicit SMTP host required (no provider auto-inference).
func (d *Dispatcher) sendEmail(target, title, body string) error {
	t, err := parseEmail(target)
	if err != nil {
		return err
	}
	msg := buildEmailMessage(t.from, t.fromName, t.to, title, body)
	return d.deliverSMTP(t, msg)
}

func parseEmail(target string) (emailTarget, error) {
	u, err := url.Parse(target)
	if err != nil {
		return emailTarget{}, fmt.Errorf("invalid email URL: %w", scrub(err))
	}
	t := emailTarget{implicitTLS: SchemeOf(target) == "mailtos"}
	t.host = u.Hostname()
	if t.host == "" {
		return emailTarget{}, fmt.Errorf("email URL needs an SMTP host: mailto://user:pass@smtp.host/?to=you@example.com")
	}
	t.port = u.Port()
	if t.port == "" {
		if t.implicitTLS {
			t.port = "465"
		} else {
			t.port = "587"
		}
	}
	if u.User != nil {
		t.username = u.User.Username()
		if p, ok := u.User.Password(); ok {
			t.password = p
		}
	}
	q := u.Query()
	t.from = strings.TrimSpace(q.Get("from"))
	if t.from == "" {
		switch {
		case strings.Contains(t.username, "@"):
			t.from = t.username
		case t.username != "":
			t.from = t.username + "@" + t.host
		default:
			t.from = "patchdeck@" + t.host
		}
	}
	t.fromName = strings.TrimSpace(q.Get("name"))
	if t.fromName == "" {
		t.fromName = "Patchdeck"
	}
	recipients := strings.TrimSpace(q.Get("to"))
	if recipients == "" {
		recipients = t.from
	}
	for _, r := range strings.Split(recipients, ",") {
		if r = strings.TrimSpace(r); r != "" {
			t.to = append(t.to, r)
		}
	}
	if len(t.to) == 0 {
		return emailTarget{}, fmt.Errorf("email URL needs a recipient: add ?to=you@example.com")
	}
	return t, nil
}

func buildEmailMessage(from, fromName string, to []string, subject, body string) []byte {
	// net/mail.Address.String() safely quotes/encodes the display name and address, so a
	// crafted name can't break out into a new header line (defense-in-depth alongside the
	// CRLF stripping below).
	fromHeader := (&mail.Address{Name: sanitizeHeader(fromName), Address: sanitizeHeader(from)}).String()
	toHeaders := make([]string, 0, len(to))
	for _, r := range to {
		toHeaders = append(toHeaders, (&mail.Address{Address: sanitizeHeader(r)}).String())
	}
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", fromHeader)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(toHeaders, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("UTF-8", sanitizeHeader(subject)))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	// Normalize body line endings to CRLF (the DotWriter from c.Data handles dot-stuffing).
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n"))
	return []byte(b.String())
}

func (d *Dispatcher) deliverSMTP(t emailTarget, msg []byte) error {
	addr := net.JoinHostPort(t.host, t.port)
	tlsCfg := &tls.Config{ServerName: t.host}

	var conn net.Conn
	var err error
	if t.implicitTLS {
		conn, err = tls.DialWithDialer(&net.Dialer{Timeout: d.timeout}, "tcp", addr, tlsCfg)
	} else {
		conn, err = net.DialTimeout("tcp", addr, d.timeout)
	}
	if err != nil {
		return fmt.Errorf("email connect to %s failed: %w", addr, scrub(err))
	}
	// Bound the whole SMTP conversation (net/smtp doesn't apply a timeout itself).
	_ = conn.SetDeadline(time.Now().Add(d.timeout))

	c, err := smtp.NewClient(conn, t.host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("email client init failed: %w", scrub(err))
	}
	defer c.Close()

	if !t.implicitTLS {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("email STARTTLS failed: %w", scrub(err))
			}
		}
	}
	if t.username != "" {
		if err := c.Auth(smtp.PlainAuth("", t.username, t.password, t.host)); err != nil {
			return fmt.Errorf("email authentication failed: %w", scrub(err))
		}
	}
	if err := c.Mail(t.from); err != nil {
		return fmt.Errorf("email sender rejected: %w", scrub(err))
	}
	for _, r := range t.to {
		if err := c.Rcpt(r); err != nil {
			return fmt.Errorf("email recipient %s rejected: %w", r, scrub(err))
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("email DATA failed: %w", scrub(err))
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("email body write failed: %w", scrub(err))
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("email send failed: %w", scrub(err))
	}
	return c.Quit()
}
