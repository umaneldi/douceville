package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/umaneldi/douceville/internal/models"
)

const bobEmail = "bob@example.com"

// adminStore renvoie un store où u1 (alice) est administratrice et u2 (bob)
// un compte au niveau prev.
func adminStore(t *testing.T, prev models.Perms) mockStore {
	t.Helper()
	st := alice(t, true)
	st.user.Perms = models.PermsAdmin
	bob := bobEmail
	st.users = []models.DvUser{*st.user, {ID: "u2", Email: &bob, Perms: prev}}
	st.permsPrev = prev
	return st
}

func TestAdmin_RequiresAdmin(t *testing.T) {
	h := &Handlers{Store: alice(t, true)} // niveau granted
	for _, rq := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/admin/users", ""},
		{http.MethodPatch, "/api/admin/users/u2", `{"perms":"granted"}`},
		{http.MethodDelete, "/api/admin/users/u2", ""},
	} {
		rr := call(t, h, rq.method, rq.path, rq.body)
		if rr.Code != http.StatusForbidden || decodeMap(t, rr)["code"] != "admin_required" {
			t.Errorf("%s %s: status = %d (body: %s), want 403 admin_required", rq.method, rq.path, rr.Code, rr.Body.String())
		}
	}
}

func TestAdmin_ListUsers(t *testing.T) {
	rr := call(t, &Handlers{Store: adminStore(t, models.PermsVerified)}, http.MethodGet, "/api/admin/users", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rr.Code, rr.Body.String())
	}
	var users []models.DvUser
	if err := json.Unmarshal(rr.Body.Bytes(), &users); err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[1].ID != "u2" || users[1].Perms != models.PermsVerified {
		t.Errorf("users = %+v", users)
	}
}

func TestAdmin_SetUserPerms(t *testing.T) {
	for _, tc := range []struct {
		prev, next models.Perms
		mails      int
	}{
		{models.PermsVerified, models.PermsGranted, 1},
		{models.PermsUnverified, models.PermsAdmin, 1},
		{models.PermsGranted, models.PermsAdmin, 0},
		{models.PermsGranted, models.PermsVerified, 0},
	} {
		st := adminStore(t, tc.prev)
		var got models.Perms
		st.setPermsTo = &got
		mailer, sent := newMailer()
		h := &Handlers{Store: st, Mailer: mailer, PublicURL: "https://dv.example"}

		rr := call(t, h, http.MethodPatch, "/api/admin/users/u2", `{"perms":"`+string(tc.next)+`"}`)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s -> %s: status = %d (body: %s)", tc.prev, tc.next, rr.Code, rr.Body.String())
		}
		if got != tc.next || decodeMap(t, rr)["perms"] != string(tc.next) {
			t.Errorf("%s -> %s: store got %q, body %s", tc.prev, tc.next, got, rr.Body.String())
		}
		if len(*sent) != tc.mails {
			t.Fatalf("%s -> %s: %d e-mails sent, want %d", tc.prev, tc.next, len(*sent), tc.mails)
		}
		if tc.mails == 1 && (*sent)[0].to != bobEmail {
			t.Errorf("approval e-mail sent to %q, want %q", (*sent)[0].to, bobEmail)
		}
	}
}

func TestAdmin_SetUserPerms_Errors(t *testing.T) {
	for _, tc := range []struct {
		path, body string
		want       int
	}{
		{"/api/admin/users/u2", `{"perms":"root"}`, http.StatusUnprocessableEntity},
		{"/api/admin/users/u2", `not json`, http.StatusBadRequest},
		{"/api/admin/users/u1", `{"perms":"granted"}`, http.StatusUnprocessableEntity},
		{"/api/admin/users/nobody", `{"perms":"granted"}`, http.StatusNotFound},
	} {
		h := &Handlers{Store: adminStore(t, models.PermsVerified)}
		rr := call(t, h, http.MethodPatch, tc.path, tc.body)
		if rr.Code != tc.want {
			t.Errorf("PATCH %s %s: status = %d, want %d", tc.path, tc.body, rr.Code, tc.want)
		}
	}
}

func TestAdmin_DeleteUser(t *testing.T) {
	st := adminStore(t, models.PermsGranted)
	var deleted string
	st.deleted = &deleted
	rr := call(t, &Handlers{Store: st}, http.MethodDelete, "/api/admin/users/u2", "")
	if rr.Code != http.StatusNoContent || deleted != "u2" {
		t.Errorf("status = %d, deleted = %q", rr.Code, deleted)
	}

	for path, want := range map[string]int{
		"/api/admin/users/u1":     http.StatusUnprocessableEntity,
		"/api/admin/users/nobody": http.StatusNotFound,
	} {
		rr := call(t, &Handlers{Store: adminStore(t, models.PermsGranted)}, http.MethodDelete, path, "")
		if rr.Code != want {
			t.Errorf("DELETE %s: status = %d, want %d", path, rr.Code, want)
		}
	}
}
