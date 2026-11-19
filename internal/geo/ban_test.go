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
