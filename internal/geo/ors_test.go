package geo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/umaneldi/douceville/internal/models"
)

// redirect renvoie un client HTTP qui envoie toutes les requêtes vers srv en
// conservant le chemin et la requête : il permet de tester les clients dont
// l'URL de base est fixe (API publique) ou dérivée d'un nom d'hôte.
func redirect(srv *httptest.Server) *http.Client {
	target, _ := url.Parse(srv.URL)
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.Header.Set("X-Original-Host", r.URL.Host)
		r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
		return http.DefaultTransport.RoundTrip(r)
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const orsTwoLevels = `{"features":[
	{"properties":{"value":600},"geometry":{"coordinates":[[[0,0],[2,0],[2,2],[0,0]]]}},
	{"properties":{"value":300},"geometry":{"coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}},
	{"properties":{"value":900},"geometry":{"coordinates":[]}}
]}`

func TestCalcIsochrone(t *testing.T) {
	var got struct {
		path, auth, accept string
		body               map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path, got.auth, got.accept = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Accept")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got.body)
		_, _ = w.Write([]byte(orsTwoLevels))
	}))
	defer srv.Close()

	c := NewORSClient("ors-key", redirect(srv))
	iso, err := c.CalcIsochrone(context.Background(), [2]float64{2.35, 48.85}, 600, 300, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.path != "/v2/isochrones/driving-car" || got.auth != "ors-key" || !strings.Contains(got.accept, "application/geo+json") {
		t.Errorf("request: path=%q auth=%q accept=%q", got.path, got.auth, got.accept)
	}
	if got.body["interval"] != 300.0 || got.body["range"].([]any)[0] != 600.0 {
		t.Errorf("payload = %v", got.body)
	}
	// Niveaux triés par temps croissant, géométrie vide ignorée ; Geometry est
	// celle du temps maximal.
	if len(iso.Levels) != 2 || iso.Levels[0].Dist != 300 || iso.Levels[1].Dist != 600 {
		t.Fatalf("levels = %+v", iso.Levels)
	}
	if iso.Transp != "driving-car" || iso.Dist != 600 || iso.LonLat != [2]float64{2.35, 48.85} || iso.Geometry[1] != [2]float64{2, 0} {
		t.Errorf("iso = %+v", iso)
	}
}

func TestCalcIsochrone_IntervalClamped(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/v2/isochrones/foot-walking" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"features":[{"properties":{"value":900},"geometry":{"coordinates":[[[0,0],[1,0],[0,0]]]}}]}`))
	}))
	defer srv.Close()

	c := NewORSClient("k", redirect(srv))
	for _, interval := range []float64{0, -5, 5000} {
		if _, err := c.CalcIsochrone(context.Background(), [2]float64{0, 0}, 900, interval, "foot-walking"); err != nil {
			t.Fatal(err)
		}
		if body["interval"] != 900.0 {
			t.Errorf("interval %v: sent %v, want 900 (single level)", interval, body["interval"])
		}
	}
}

func TestCalcIsochrone_Errors(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"http error":  {http.StatusForbidden, `{}`},
		"bad json":    {http.StatusOK, `{`},
		"no geometry": {http.StatusOK, `{"features":[]}`},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			if _, err := NewORSClient("k", redirect(srv)).CalcIsochrone(context.Background(), [2]float64{}, 600, 0, ""); err == nil {
				t.Error("expected an error")
			}
		})
	}

	// Serveur injoignable.
	srv := httptest.NewServer(http.NotFoundHandler())
	client := redirect(srv)
	srv.Close()
	if _, err := NewORSClient("k", client).CalcIsochrone(context.Background(), [2]float64{}, 600, 0, ""); err == nil {
		t.Error("unreachable server: expected an error")
	}
}

func TestNewORSClientDefaultHTTP(t *testing.T) {
	if c := NewORSClient("k", nil); c.http != http.DefaultClient {
		t.Error("nil client should default to http.DefaultClient")
	}
}

func TestLevelInterval(t *testing.T) {
	for dist, want := range map[float64]float64{300: 300, 1800: 300, 3000: 300, 3001: 600, 5400: 600} {
		if got := LevelInterval(dist); got != want {
			t.Errorf("LevelInterval(%v) = %v, want %v", dist, got, want)
		}
	}
}

func TestPolygonWKT(t *testing.T) {
	ring := [][2]float64{{2.5, 48}, {3, 48.25}, {2.5, 48}}
	want := "POLYGON((2.500000 48.000000,3.000000 48.250000,2.500000 48.000000))"
	if got := RingWKT(ring); got != want {
		t.Errorf("RingWKT = %q, want %q", got, want)
	}
	if got := PolygonWKT(models.Isochrone{Geometry: ring}); got != want {
		t.Errorf("PolygonWKT = %q", got)
	}
	if got := RingWKT(nil); got != "POLYGON(())" {
		t.Errorf("empty ring = %q", got)
	}
}
