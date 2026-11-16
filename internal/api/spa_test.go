package api

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ydethe/douceville/web"
)

func TestSPAIndexShowsVersion(t *testing.T) {
	h := spaHandler()
	for _, path := range []string{"/", "/index.html", "/some/spa/route"} {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest("GET", path, nil))
		body, _ := io.ReadAll(rec.Body)
		want := "<title>Douceville " + web.Version + " — "
		if !strings.Contains(string(body), want) {
			t.Errorf("%s: title with version %q not found", path, web.Version)
		}
		if strings.Contains(string(body), "{{VERSION}}") {
			t.Errorf("%s: placeholder not replaced", path)
		}
	}
}
