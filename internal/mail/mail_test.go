package mail

import (
	"net/smtp"
	"testing"
)

func TestChooseAuth(t *testing.T) {
	cases := []struct {
		advertised string
		want       string
		wantErr    bool
	}{
		{"PLAIN LOGIN", "PLAIN", false},
		{"LOGIN", "LOGIN", false},
		{"login cram-md5", "LOGIN", false},
		{"CRAM-MD5", "", true},
	}
	server := &smtp.ServerInfo{Name: "mail.example.com", TLS: true}
	for _, tc := range cases {
		a, err := chooseAuth(tc.advertised, "u", "p", "mail.example.com")
		if tc.wantErr {
			if err == nil {
				t.Errorf("%q: expected error", tc.advertised)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: %v", tc.advertised, err)
		}
		mech, _, err := a.Start(server)
		if err != nil || mech != tc.want {
			t.Errorf("%q: mech = %q (err %v), want %q", tc.advertised, mech, err, tc.want)
		}
	}
}

func TestLoginAuth(t *testing.T) {
	a := &loginAuth{username: "alice", password: "secret", host: "mail.example.com"}

	if _, _, err := a.Start(&smtp.ServerInfo{Name: "mail.example.com", TLS: false}); err == nil {
		t.Error("expected refusal over an unencrypted connection")
	}
	if _, _, err := a.Start(&smtp.ServerInfo{Name: "mail.example.com", TLS: true}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// net/smtp passe au client les challenges déjà décodés de base64.
	for challenge, want := range map[string]string{"Username:": "alice", "Password:": "secret"} {
		got, err := a.Next([]byte(challenge), true)
		if err != nil || string(got) != want {
			t.Errorf("Next(%q) = %q, %v; want %q", challenge, got, err, want)
		}
	}
	if _, err := a.Next([]byte("Other:"), true); err == nil {
		t.Error("expected error on unexpected challenge")
	}
}
