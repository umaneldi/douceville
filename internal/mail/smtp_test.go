package mail

import (
	"bufio"
	"context"
	"encoding/base64"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP est un serveur SMTP minimal (sans TLS) qui enregistre la session.
type fakeSMTP struct {
	ln net.Listener
	// ehlo est la liste des extensions annoncées (ex. "AUTH PLAIN LOGIN").
	ehlo []string
	// rejectRcpt fait échouer RCPT TO.
	rejectRcpt bool

	mu       sync.Mutex
	auth     string // identifiants PLAIN décodés ("\x00user\x00pass")
	mailFrom string
	rcptTo   string
	data     string
}

func newFakeSMTP(t *testing.T, ehlo ...string) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeSMTP{ln: ln, ehlo: ehlo}
	t.Cleanup(func() { ln.Close() })
	go s.serve()
	return s
}

func (s *fakeSMTP) port() string {
	_, p, _ := net.SplitHostPort(s.ln.Addr().String())
	return p
}

func (s *fakeSMTP) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *fakeSMTP) handle(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	reply := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
	reply("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(cmd)
		switch {
		case strings.HasPrefix(upper, "EHLO"):
			if len(s.ehlo) == 0 {
				reply("250 fake")
				continue
			}
			reply("250-fake")
			for i, e := range s.ehlo {
				if i == len(s.ehlo)-1 {
					reply("250 " + e)
				} else {
					reply("250-" + e)
				}
			}
		case strings.HasPrefix(upper, "AUTH PLAIN "):
			dec, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(cmd[len("AUTH PLAIN "):]))
			s.mu.Lock()
			s.auth = string(dec)
			s.mu.Unlock()
			reply("235 ok")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			s.mu.Lock()
			s.mailFrom = cmd[len("MAIL FROM:"):]
			s.mu.Unlock()
			reply("250 ok")
		case strings.HasPrefix(upper, "RCPT TO:"):
			if s.rejectRcpt {
				reply("550 no such user")
				continue
			}
			s.mu.Lock()
			s.rcptTo = cmd[len("RCPT TO:"):]
			s.mu.Unlock()
			reply("250 ok")
		case upper == "DATA":
			reply("354 go ahead")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			s.mu.Lock()
			s.data = b.String()
			s.mu.Unlock()
			reply("250 queued")
		case upper == "QUIT":
			reply("221 bye")
			return
		default:
			reply("502 unsupported")
		}
	}
}

func (s *fakeSMTP) snapshot() (auth, from, to, data string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.auth, s.mailFrom, s.rcptTo, s.data
}

func TestSMTPSender_Send(t *testing.T) {
	srv := newFakeSMTP(t, "AUTH PLAIN LOGIN")
	sender := NewSMTPSender(SMTPConfig{
		Host: "127.0.0.1", Port: srv.port(), Username: "user", Password: "pass",
		From: "Douceville <no-reply@example.com>",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sender.Send(ctx, "alice@example.com", "Vérifiez votre adresse", "Bonjour,\nCliquez ici."); err != nil {
		t.Fatal(err)
	}

	auth, from, to, data := srv.snapshot()
	if auth != "\x00user\x00pass" {
		t.Errorf("auth = %q", auth)
	}
	if from != "<no-reply@example.com>" || to != "<alice@example.com>" {
		t.Errorf("envelope: from %q, to %q", from, to)
	}
	for _, want := range []string{
		"From: Douceville <no-reply@example.com>\r\n",
		"To: alice@example.com\r\n",
		"Subject: =?utf-8?q?V=C3=A9rifiez_votre_adresse?=\r\n",
		"Content-Type: text/plain; charset=utf-8\r\n",
		"\r\n\r\nBonjour,\r\nCliquez ici.",
	} {
		if !strings.Contains(data, want) {
			t.Errorf("message lacks %q:\n%s", want, data)
		}
	}
}

func TestSMTPSender_NoAuth(t *testing.T) {
	srv := newFakeSMTP(t) // aucune extension
	sender := NewSMTPSender(SMTPConfig{Host: "127.0.0.1", Port: srv.port(), From: "a@example.com"})
	if err := sender.Send(context.Background(), "b@example.com", "s", "b"); err != nil {
		t.Fatal(err)
	}
	if auth, from, _, _ := srv.snapshot(); auth != "" || from != "<a@example.com>" {
		t.Errorf("auth = %q, from = %q", auth, from)
	}
}

func TestSMTPSender_Errors(t *testing.T) {
	ctx := context.Background()

	if err := NewSMTPSender(SMTPConfig{Host: "127.0.0.1", Port: "1"}).Send(ctx, "a@b.c\r\nBcc: x@y.z", "s", "b"); err == nil {
		t.Error("header injection in recipient: expected an error")
	}

	// Authentification demandée mais non proposée par le serveur.
	srv := newFakeSMTP(t)
	err := NewSMTPSender(SMTPConfig{Host: "127.0.0.1", Port: srv.port(), Username: "u", From: "a@b.c"}).Send(ctx, "x@y.z", "s", "b")
	if err == nil || !strings.Contains(err.Error(), "does not support authentication") {
		t.Errorf("no AUTH: err = %v", err)
	}

	// Mécanisme non pris en charge.
	srv = newFakeSMTP(t, "AUTH CRAM-MD5")
	if err := NewSMTPSender(SMTPConfig{Host: "127.0.0.1", Port: srv.port(), Username: "u", From: "a@b.c"}).Send(ctx, "x@y.z", "s", "b"); err == nil {
		t.Error("unsupported mechanism: expected an error")
	}

	// Destinataire refusé.
	srv = newFakeSMTP(t)
	srv.rejectRcpt = true
	err = NewSMTPSender(SMTPConfig{Host: "127.0.0.1", Port: srv.port(), From: "a@b.c"}).Send(ctx, "x@y.z", "s", "b")
	if err == nil || !strings.Contains(err.Error(), "RCPT TO") {
		t.Errorf("rejected recipient: err = %v", err)
	}

	// Serveur injoignable.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	ln.Close()
	if err := NewSMTPSender(SMTPConfig{Host: "127.0.0.1", Port: port, From: "a@b.c"}).Send(ctx, "x@y.z", "s", "b"); err == nil {
		t.Error("unreachable server: expected an error")
	}
}

func TestEnvelopeAddress(t *testing.T) {
	for in, want := range map[string]string{
		"Douceville <no-reply@example.com>": "no-reply@example.com",
		"  plain@example.com ":              "plain@example.com",
		"Broken <x@example.com":             "Broken <x@example.com",
	} {
		if got := envelopeAddress(in); got != want {
			t.Errorf("envelopeAddress(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildMessageNormalizesNewlines(t *testing.T) {
	msg := string(buildMessage("a@b.c", "d@e.f", "Hello", "l1\nl2\r\nl3"))
	if !strings.HasSuffix(msg, "\r\n\r\nl1\r\nl2\r\nl3") {
		t.Errorf("body not CRLF-normalized: %q", msg)
	}
	if !strings.Contains(msg, "Subject: Hello\r\n") || !strings.Contains(msg, "MIME-Version: 1.0\r\n") {
		t.Errorf("headers: %q", msg)
	}
}

func TestLogSender(t *testing.T) {
	if err := (LogSender{}).Send(context.Background(), "a@b.c", "s", "b"); err != nil {
		t.Errorf("LogSender.Send = %v", err)
	}
}
