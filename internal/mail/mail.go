// Package mail envoie les e-mails transactionnels (vérification d'adresse).
package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Sender envoie un e-mail texte brut.
type Sender interface {
	Send(ctx context.Context, to, subject, body string) error
}

// SMTPConfig décrit un serveur SMTP. Le port 465 utilise TLS implicite ; les
// autres ports utilisent STARTTLS si le serveur le propose.
type SMTPConfig struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string
}

// SMTPSender envoie les e-mails via un serveur SMTP.
type SMTPSender struct {
	cfg SMTPConfig
}

// NewSMTPSender crée un SMTPSender.
func NewSMTPSender(cfg SMTPConfig) *SMTPSender {
	return &SMTPSender{cfg: cfg}
}

// Send implémente Sender.
func (s *SMTPSender) Send(ctx context.Context, to, subject, body string) error {
	if strings.ContainsAny(to, "\r\n") {
		return fmt.Errorf("invalid recipient")
	}
	msg := buildMessage(s.cfg.From, to, subject, body)
	addr := net.JoinHostPort(s.cfg.Host, s.cfg.Port)

	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	var err error
	if s.cfg.Port == "465" {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: s.cfg.Host}}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	}

	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp client: %w", err)
	}
	defer c.Close()

	if s.cfg.Port != "465" {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: s.cfg.Host}); err != nil {
				return fmt.Errorf("smtp starttls: %w", err)
			}
		}
	}
	if s.cfg.Username != "" {
		ok, mechs := c.Extension("AUTH")
		if !ok {
			return fmt.Errorf("smtp auth: server does not support authentication")
		}
		a, err := chooseAuth(mechs, s.cfg.Username, s.cfg.Password, s.cfg.Host)
		if err != nil {
			return err
		}
		if err := c.Auth(a); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(envelopeAddress(s.cfg.From)); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("smtp RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}
	return c.Quit()
}

// chooseAuth sélectionne un mécanisme parmi ceux annoncés par le serveur
// (paramètre de l'extension AUTH), par ordre de préférence : PLAIN, LOGIN.
func chooseAuth(advertised, username, password, host string) (smtp.Auth, error) {
	mechs := map[string]bool{}
	for _, m := range strings.Fields(strings.ToUpper(advertised)) {
		mechs[m] = true
	}
	switch {
	case mechs["PLAIN"]:
		return smtp.PlainAuth("", username, password, host), nil
	case mechs["LOGIN"]:
		return &loginAuth{username: username, password: password, host: host}, nil
	default:
		return nil, fmt.Errorf("smtp auth: no supported mechanism (server offers %q)", advertised)
	}
}

// loginAuth implémente le mécanisme AUTH LOGIN (non fourni par net/smtp).
// Comme smtp.PlainAuth, il refuse d'envoyer les identifiants en clair hors TLS,
// sauf vers localhost.
type loginAuth struct {
	username, password, host string
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && !isLocalhost(server.Name) {
		return "", nil, fmt.Errorf("unencrypted connection")
	}
	if server.Name != a.host {
		return "", nil, fmt.Errorf("wrong host name")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch prompt := strings.ToLower(strings.TrimSpace(string(fromServer))); {
	case strings.HasPrefix(prompt, "username"):
		return []byte(a.username), nil
	case strings.HasPrefix(prompt, "password"):
		return []byte(a.password), nil
	default:
		return nil, fmt.Errorf("unexpected LOGIN challenge %q", fromServer)
	}
}

func isLocalhost(name string) bool {
	return name == "localhost" || name == "127.0.0.1" || name == "::1"
}

// envelopeAddress extrait l'adresse nue d'un expéditeur "Nom <adresse>".
func envelopeAddress(from string) string {
	if i := strings.LastIndex(from, "<"); i >= 0 {
		if j := strings.LastIndex(from, ">"); j > i {
			return from[i+1 : j]
		}
	}
	return strings.TrimSpace(from)
}

func buildMessage(from, to, subject, body string) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n"))
	return []byte(b.String())
}

// LogSender n'envoie rien : il journalise l'e-mail. Utile en développement,
// quand aucun serveur SMTP n'est configuré.
type LogSender struct{}

// Send implémente Sender.
func (LogSender) Send(ctx context.Context, to, subject, body string) error {
	slog.Warn("SMTP not configured, e-mail not sent (logged instead)", "to", to, "subject", subject, "body", body)
	return nil
}
