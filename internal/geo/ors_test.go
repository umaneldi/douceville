package geo

import (
	"context"
	"encoding/json"
	"fmt"
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
		if r.Header.Get("X-Original-Host") != "api.heigit.org" ||
			!strings.HasPrefix(r.URL.Path, "/openrouteservice/v2/isochrones/") {
			w.WriteHeader(http.StatusGone)
			return
		}
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
	if got.path != "/openrouteservice/v2/isochrones/driving-car" || got.auth != "ors-key" || !strings.Contains(got.accept, "application/geo+json") {
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
		if r.URL.Path != "/openrouteservice/v2/isochrones/foot-walking" {
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

func TestCalcIsochrones_Batches(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openrouteservice/v2/isochrones/foot-walking" || r.Header.Get("Authorization") != "k" {
			t.Errorf("request: path=%q auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body struct {
			Locations [][2]float64 `json:"locations"`
			Range     []float64    `json:"range"`
		}
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		bodies = append(bodies, m)
		_ = json.Unmarshal(b, &body)
		// Réponse dans le désordre : chaque anneau commence par sa position.
		var feats []string
		for i := len(body.Locations) - 1; i >= 0; i-- {
			l := body.Locations[i]
			feats = append(feats, fmt.Sprintf(`{"properties":{"value":%v,"group_index":%d},"geometry":{"coordinates":[[[%v,%v],[0,0],[%v,%v]]]}}`,
				body.Range[0], i, l[0], l[1], l[0], l[1]))
		}
		_, _ = w.Write([]byte(`{"features":[` + strings.Join(feats, ",") + `]}`))
	}))
	defer srv.Close()

	var lonlats [][2]float64
	for i := range 7 {
		lonlats = append(lonlats, [2]float64{float64(i), float64(10 + i)})
	}
	isos, err := NewORSClient("k", redirect(srv)).CalcIsochrones(context.Background(), lonlats, 600, "foot-walking")
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || len(bodies[0]["locations"].([]any)) != 5 || len(bodies[1]["locations"].([]any)) != 2 {
		t.Fatalf("requests = %v, want 2 batches of 5 and 2 locations", bodies)
	}
	if _, ok := bodies[0]["interval"]; ok {
		t.Errorf("no interval expected: %v", bodies[0])
	}
	if len(isos) != 7 {
		t.Fatalf("got %d isochrones, want 7", len(isos))
	}
	for i, iso := range isos {
		if iso.LonLat != lonlats[i] || iso.Geometry[0] != lonlats[i] || iso.Dist != 600 || iso.Transp != "foot-walking" || iso.Levels != nil {
			t.Errorf("iso %d = %+v, want centred on %v", i, iso, lonlats[i])
		}
	}

	// Aucune position : aucune requête.
	bodies = nil
	if isos, err := NewORSClient("k", redirect(srv)).CalcIsochrones(context.Background(), nil, 600, "foot-walking"); err != nil || len(isos) != 0 || len(bodies) != 0 {
		t.Errorf("no location = %v, %v (%d requests)", isos, err, len(bodies))
	}
}

func TestCalcIsochrones_Errors(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"http error":       {http.StatusTooManyRequests, `{}`},
		"invalid json":     {http.StatusOK, `{`},
		"missing location": {http.StatusOK, `{"features":[{"properties":{"value":600,"group_index":0},"geometry":{"coordinates":[[[0,0],[1,1],[0,0]]]}}]}`},
		"empty geometry":   {http.StatusOK, `{"features":[{"properties":{"value":600,"group_index":0},"geometry":{"coordinates":[]}},{"properties":{"value":600,"group_index":1},"geometry":{"coordinates":[[[0,0]]]}}]}`},
		"bad group index":  {http.StatusOK, `{"features":[{"properties":{"value":600,"group_index":7},"geometry":{"coordinates":[[[0,0]]]}}]}`},
	} {
		t.Run(name, func(t *testing.T) {
			var path string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			_, err := NewORSClient("k", redirect(srv)).CalcIsochrones(context.Background(), [][2]float64{{1, 2}, {3, 4}}, 600, "")
			if err == nil {
				t.Error("expected an error")
			}
			if path != "/openrouteservice/v2/isochrones/driving-car" {
				t.Errorf("path = %q, want default profile driving-car", path)
			}
		})
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

func TestCalcItineraire(t *testing.T) {
	var got struct {
		host, path, auth string
		body             map[string][][2]float64
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.host, got.path, got.auth = r.Header.Get("X-Original-Host"), r.URL.Path, r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got.body)
		_, _ = w.Write([]byte(`{"features":[{"properties":{"summary":{"distance":1234.5,"duration":678.9}},
			"geometry":{"coordinates":[[1.44,43.6],[1.45,43.61],[1.46,43.62]]}}]}`))
	}))
	defer srv.Close()

	from, to := [2]float64{1.44, 43.6}, [2]float64{1.46, 43.62}
	it, err := NewORSClient("ors-key", redirect(srv)).CalcItineraire(context.Background(), from, to, "foot-walking")
	if err != nil {
		t.Fatalf("CalcItineraire: %v", err)
	}
	if got.host != "api.heigit.org" || got.path != "/openrouteservice/v2/directions/foot-walking/geojson" {
		t.Errorf("url = %s%s, want api.heigit.org/openrouteservice/v2/directions/foot-walking/geojson", got.host, got.path)
	}
	if got.auth != "ors-key" {
		t.Errorf("Authorization = %q, want ors-key", got.auth)
	}
	if c := got.body["coordinates"]; len(c) != 2 || c[0] != from || c[1] != to {
		t.Errorf("coordinates = %v, want [%v %v]", c, from, to)
	}
	if len(it.Geometry) != 3 || it.Geometry[2] != to || it.DistanceM != 1234.5 || it.DureeS != 678.9 {
		t.Errorf("itineraire = %+v", it)
	}

	// Profil par défaut.
	if _, err := NewORSClient("k", redirect(srv)).CalcItineraire(context.Background(), from, to, ""); err != nil {
		t.Fatalf("CalcItineraire (default): %v", err)
	}
	if got.path != "/openrouteservice/v2/directions/driving-car/geojson" {
		t.Errorf("default path = %s, want driving-car", got.path)
	}
}

func TestCalcItineraire_Errors(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"http error":     {http.StatusNotFound, `{}`},
		"bad json":       {http.StatusOK, `{`},
		"no feature":     {http.StatusOK, `{"features":[]}`},
		"empty geometry": {http.StatusOK, `{"features":[{"geometry":{"coordinates":[]}}]}`},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			if _, err := NewORSClient("k", redirect(srv)).CalcItineraire(context.Background(), [2]float64{}, [2]float64{1, 1}, ""); err == nil {
				t.Error("expected an error")
			}
		})
	}

	// Serveur injoignable.
	srv := httptest.NewServer(http.NotFoundHandler())
	client := redirect(srv)
	srv.Close()
	if _, err := NewORSClient("k", client).CalcItineraire(context.Background(), [2]float64{}, [2]float64{1, 1}, ""); err == nil {
		t.Error("unreachable server: expected an error")
	}
}
