package geo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSuggest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("autocomplete") != "1" || q.Get("limit") != "5" || q.Get("q") != "12 rue de la pa" {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"features":[{"properties":{"label":"12 Rue de la Paix 75002 Paris"},"geometry":{"coordinates":[2.33,48.87]}}]}`))
	}))
	defer srv.Close()

	c := &BANClient{baseURL: srv.URL, http: srv.Client()}
	got, err := c.Suggest(context.Background(), "12 rue de la pa", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Label != "12 Rue de la Paix 75002 Paris" || got[0].Longitude != 2.33 || got[0].Latitude != 48.87 {
		t.Errorf("got %+v", got)
	}
}

func TestGeocode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/search" || (q.Get("limit") == "1") == q.Has("autocomplete") || r.Header.Get("X-Original-Host") != "addok.local:7878" {
			t.Errorf("unexpected request: %s %s (host %s)", r.URL.Path, r.URL.RawQuery, r.Header.Get("X-Original-Host"))
		}
		switch q.Get("q") {
		case "nowhere":
			_, _ = w.Write([]byte(`{"features":[]}`))
		case "boom":
			w.WriteHeader(http.StatusInternalServerError)
		case "garbage":
			_, _ = w.Write([]byte(`not json`))
		default:
			_, _ = w.Write([]byte(`{"features":[{"geometry":{"coordinates":[2.35,48.85]}},{"geometry":{"coordinates":[0,0]}}]}`))
		}
	}))
	defer srv.Close()

	c := NewBANClient("addok.local", redirect(srv))
	got, err := c.Geocode(context.Background(), "1 rue de la Paix")
	if err != nil || got.Longitude != 2.35 || got.Latitude != 48.85 {
		t.Errorf("Geocode = %+v, %v", got, err)
	}
	for _, q := range []string{"nowhere", "boom", "garbage"} {
		if _, err := c.Geocode(context.Background(), q); err == nil {
			t.Errorf("%s: expected an error", q)
		}
	}
	if _, err := c.Suggest(context.Background(), "boom", 5); err == nil {
		t.Error("Suggest: expected an error")
	}
}

func TestNewBANClient(t *testing.T) {
	if c := NewBANClient("", nil); c.baseURL != PublicBANURL || c.http == nil || c.http.Timeout == 0 {
		t.Errorf("public client = %+v", c)
	}
	if c := NewBANClient("addok", nil); c.baseURL != "http://addok:7878" {
		t.Errorf("self-hosted baseURL = %q", c.baseURL)
	}
}
