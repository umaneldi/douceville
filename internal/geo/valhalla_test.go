package geo

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// encodePolyline6 encode des positions [lon, lat] en polyligne (précision
// 1e-6), pour fabriquer les réponses de /route.
func encodePolyline6(pts [][2]float64) string {
	var b strings.Builder
	var plat, plon int64
	enc := func(v int64) {
		u := v << 1
		if v < 0 {
			u = ^u
		}
		for u >= 0x20 {
			b.WriteByte(byte((0x20 | (u & 0x1f)) + 63))
			u >>= 5
		}
		b.WriteByte(byte(u + 63))
	}
	for _, p := range pts {
		lat, lon := int64(math.Round(p[1]*1e6)), int64(math.Round(p[0]*1e6))
		enc(lat - plat)
		enc(lon - plon)
		plat, plon = lat, lon
	}
	return b.String()
}

// valhallaServer démarre un faux Valhalla : h reçoit le chemin et le corps
// JSON décodé de chaque requête.
func valhallaServer(t *testing.T, h func(w http.ResponseWriter, path string, body map[string]any)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("method %s, content-type %q", r.Method, r.Header.Get("Content-Type"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		h(w, r.URL.Path, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

const square = `[[[1,1],[2,1],[2,2],[1,2],[1,1]]]`

func TestValhallaCalcIsochrone(t *testing.T) {
	var got map[string]any
	srv := valhallaServer(t, func(w http.ResponseWriter, path string, body map[string]any) {
		if path != "/isochrone" {
			t.Errorf("path = %s, want /isochrone", path)
		}
		got = body
		// Contours renvoyés dans le désordre, l'un en MultiPolygon.
		_, _ = w.Write([]byte(`{"type":"FeatureCollection","features":[
			{"properties":{"contour":20},"geometry":{"type":"MultiPolygon","coordinates":[
				[[[0,0],[0.1,0],[0.1,0.1],[0,0]]],
				[[[0,0],[5,0],[5,5],[0,5],[0,0]]]]}},
			{"properties":{"contour":10},"geometry":{"type":"Polygon","coordinates":` + square + `}}]}`))
	})
	c := NewValhallaClient(srv.URL+"/", 0, nil)
	iso, err := c.CalcIsochrone(context.Background(), [2]float64{1.44, 43.6}, 1200, 600, "cycling-regular")
	if err != nil {
		t.Fatalf("CalcIsochrone: %v", err)
	}
	if got["costing"] != "bicycle" || got["polygons"] != true {
		t.Errorf("body = %v, want costing bicycle, polygons true", got)
	}
	loc := got["locations"].([]any)[0].(map[string]any)
	if loc["lat"] != 43.6 || loc["lon"] != 1.44 {
		t.Errorf("location = %v, want lat 43.6 lon 1.44", loc)
	}
	contours := got["contours"].([]any)
	if len(contours) != 2 || contours[0].(map[string]any)["time"] != 10.0 || contours[1].(map[string]any)["time"] != 20.0 {
		t.Errorf("contours = %v, want 10 and 20 minutes", contours)
	}
	if len(iso.Levels) != 2 || iso.Levels[0].Dist != 600 || iso.Levels[1].Dist != 1200 {
		t.Fatalf("levels = %+v, want 600 s then 1200 s", iso.Levels)
	}
	// Le plus grand polygone du MultiPolygon est retenu.
	if len(iso.Geometry) != 5 || iso.Geometry[1] != [2]float64{5, 0} {
		t.Errorf("geometry = %v, want the 5x5 square", iso.Geometry)
	}
	if iso.Transp != "cycling-regular" || iso.Dist != 1200 || iso.LonLat != [2]float64{1.44, 43.6} {
		t.Errorf("iso = %+v", iso)
	}
}

func TestValhallaCalcIsochrone_SingleLevelAndDefaults(t *testing.T) {
	var got map[string]any
	srv := valhallaServer(t, func(w http.ResponseWriter, _ string, body map[string]any) {
		got = body
		_, _ = w.Write([]byte(`{"features":[{"properties":{"contour":15},"geometry":{"type":"Polygon","coordinates":` + square + `}}]}`))
	})
	// Pas absent ou supérieur au temps : un seul contour ; profil par défaut auto.
	for _, interval := range []float64{0, 5000} {
		iso, err := NewValhallaClient(srv.URL, 2, srv.Client()).CalcIsochrone(context.Background(), [2]float64{1, 2}, 900, interval, "")
		if err != nil || len(iso.Levels) != 1 || iso.Transp != "driving-car" {
			t.Fatalf("interval %v: iso = %+v, %v", interval, iso, err)
		}
		if c := got["contours"].([]any); len(c) != 1 || c[0].(map[string]any)["time"] != 15.0 || got["costing"] != "auto" {
			t.Errorf("interval %v: body = %v, want one 15-minute auto contour", interval, got)
		}
	}
}

func TestValhallaCosting(t *testing.T) {
	for transp, want := range map[string]string{
		"driving-car": "auto", "cycling-regular": "bicycle", "foot-walking": "pedestrian", "": "auto", "inconnu": "auto",
	} {
		if got := costing(transp); got != want {
			t.Errorf("costing(%q) = %q, want %q", transp, got, want)
		}
	}
}

func TestValhallaErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"erreur Valhalla", `{"error_code":171,"error":"No suitable edges near location"}`, "status 400: No suitable edges", 400},
		{"erreur sans corps JSON", `oops`, "status 502", 502},
		{"JSON invalide", `{`, "", 200},
		{"sans géométrie", `{"features":[{"properties":{"contour":10},"geometry":{"type":"Polygon","coordinates":[]}}]}`, "no isochrone geometry", 200},
		{"géométrie inattendue", `{"features":[{"properties":{"contour":10},"geometry":{"type":"LineString","coordinates":[[0,0],[1,1]]}}]}`, "unexpected geometry", 200},
		{"coordonnées invalides", `{"features":[{"properties":{"contour":10},"geometry":{"type":"Polygon","coordinates":"x"}}]}`, "", 200},
		{"multipolygone invalide", `{"features":[{"properties":{"contour":10},"geometry":{"type":"MultiPolygon","coordinates":"x"}}]}`, "", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := valhallaServer(t, func(w http.ResponseWriter, _ string, _ map[string]any) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			_, err := NewValhallaClient(srv.URL, 1, nil).CalcIsochrone(context.Background(), [2]float64{1, 2}, 600, 0, "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
	// Service injoignable.
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	if _, err := NewValhallaClient(srv.URL, 1, nil).CalcIsochrone(context.Background(), [2]float64{1, 2}, 600, 0, ""); err == nil {
		t.Error("unreachable service: want an error")
	}
}

func TestValhallaCalcIsochrones_OrderAndConcurrency(t *testing.T) {
	var inFlight, maxInFlight atomic.Int32
	srv := valhallaServer(t, func(w http.ResponseWriter, _ string, body map[string]any) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			m := maxInFlight.Load()
			if n <= m || maxInFlight.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		// L'anneau renvoyé encode la position demandée, pour vérifier l'ordre.
		loc := body["locations"].([]any)[0].(map[string]any)
		ring, _ := json.Marshal([][][2]float64{{{loc["lon"].(float64), loc["lat"].(float64)}, {0, 0}, {1, 1}}})
		_, _ = w.Write([]byte(`{"features":[{"properties":{"contour":10},"geometry":{"type":"Polygon","coordinates":` + string(ring) + `}}]}`))
	})
	lonlats := make([][2]float64, 9)
	for i := range lonlats {
		lonlats[i] = [2]float64{float64(i), float64(-i)}
	}
	isos, err := NewValhallaClient(srv.URL, 3, nil).CalcIsochrones(context.Background(), lonlats, 600, "foot-walking")
	if err != nil {
		t.Fatalf("CalcIsochrones: %v", err)
	}
	for i, iso := range isos {
		if iso.LonLat != lonlats[i] || iso.Geometry[0] != lonlats[i] || iso.Transp != "foot-walking" || iso.Dist != 600 {
			t.Errorf("isos[%d] = %+v, want location %v", i, iso, lonlats[i])
		}
	}
	if m := maxInFlight.Load(); m > 3 || m < 2 {
		t.Errorf("max concurrent requests = %d, want 2..3 (limit 3)", m)
	}

	// Aucune position : aucune requête.
	if isos, err := NewValhallaClient(srv.URL, 3, nil).CalcIsochrones(context.Background(), nil, 600, ""); err != nil || len(isos) != 0 {
		t.Errorf("empty = %v, %v", isos, err)
	}
}

func TestValhallaCalcIsochrones_Error(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := valhallaServer(t, func(w http.ResponseWriter, _ string, body map[string]any) {
		mu.Lock()
		calls++
		mu.Unlock()
		if body["locations"].([]any)[0].(map[string]any)["lon"] == 2.0 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"No suitable edges near location"}`))
			return
		}
		_, _ = w.Write([]byte(`{"features":[{"properties":{"contour":10},"geometry":{"type":"Polygon","coordinates":` + square + `}}]}`))
	})
	_, err := NewValhallaClient(srv.URL, 1, nil).CalcIsochrones(context.Background(), [][2]float64{{1, 1}, {2, 2}, {3, 3}}, 600, "")
	if err == nil || !strings.Contains(err.Error(), "location 1") || !strings.Contains(err.Error(), "No suitable edges") {
		t.Errorf("err = %v, want location 1 error", err)
	}
}

func TestValhallaCalcItineraire(t *testing.T) {
	leg1 := [][2]float64{{1.444, 43.604}, {1.45, 43.61}}
	leg2 := [][2]float64{{1.46, 43.62}, {-0.5, -1.25}}
	var got map[string]any
	srv := valhallaServer(t, func(w http.ResponseWriter, path string, body map[string]any) {
		if path != "/route" {
			t.Errorf("path = %s, want /route", path)
		}
		got = body
		resp, _ := json.Marshal(map[string]any{"trip": map[string]any{
			"legs":    []map[string]string{{"shape": encodePolyline6(leg1)}, {"shape": encodePolyline6(leg2)}},
			"summary": map[string]float64{"length": 2.5, "time": 600},
		}})
		_, _ = w.Write(resp)
	})
	it, err := NewValhallaClient(srv.URL, 1, nil).CalcItineraire(context.Background(), [2]float64{1.444, 43.604}, [2]float64{-0.5, -1.25}, "foot-walking")
	if err != nil {
		t.Fatalf("CalcItineraire: %v", err)
	}
	if got["costing"] != "pedestrian" || got["units"] != "kilometers" || got["directions_type"] != "none" {
		t.Errorf("body = %v", got)
	}
	if locs := got["locations"].([]any); len(locs) != 2 || locs[1].(map[string]any)["lat"] != -1.25 {
		t.Errorf("locations = %v", locs)
	}
	if it.DistanceM != 2500 || it.DureeS != 600 || len(it.Geometry) != 4 {
		t.Fatalf("itineraire = %+v, want 2500 m, 600 s, 4 points", it)
	}
	for i, want := range append(leg1, leg2...) {
		if math.Abs(it.Geometry[i][0]-want[0]) > 1e-9 || math.Abs(it.Geometry[i][1]-want[1]) > 1e-9 {
			t.Errorf("geometry[%d] = %v, want %v", i, it.Geometry[i], want)
		}
	}
}

func TestValhallaCalcItineraire_Errors(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"sans tronçon", `{"trip":{"legs":[],"summary":{"length":0,"time":0}}}`, "no route"},
		{"tracé invalide", `{"trip":{"legs":[{"shape":"\u0001"}]}}`, "invalid polyline"},
		{"JSON invalide", `{`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := valhallaServer(t, func(w http.ResponseWriter, _ string, _ map[string]any) { _, _ = w.Write([]byte(tc.body)) })
			if _, err := NewValhallaClient(srv.URL, 1, nil).CalcItineraire(context.Background(), [2]float64{}, [2]float64{1, 1}, ""); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestDecodePolyline6(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		want     [][2]float64
		err      bool
	}{
		// Exemple de référence de Google (précision 5) lu en précision 6.
		{"référence", "_p~iF~ps|U_ulLnnqC_mqNvxq`@", [][2]float64{{-12.02, 3.85}, {-12.095, 4.07}, {-12.6453, 4.3252}}, false},
		{"vide", "", nil, false},
		{"tronqué", "_p~iF~ps|", nil, true},
		{"caractère invalide", " ", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodePolyline6(tc.in)
			if (err != nil) != tc.err {
				t.Fatalf("err = %v, want error %v", err, tc.err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if math.Abs(got[i][0]-tc.want[i][0]) > 1e-9 || math.Abs(got[i][1]-tc.want[i][1]) > 1e-9 {
					t.Errorf("point %d = %v, want %v", i, got[i], tc.want[i])
				}
			}
		})
	}
	// Aller-retour avec l'encodeur de test.
	pts := [][2]float64{{2.351234, 48.856614}, {-61.533333, 16.25}, {55.45, -20.88}}
	if got, err := decodePolyline6(encodePolyline6(pts)); err != nil || len(got) != 3 || math.Abs(got[1][0]-pts[1][0]) > 1e-6 {
		t.Errorf("round trip = %v, %v", got, err)
	}
}

func TestRingArea(t *testing.T) {
	if a := ringArea([][2]float64{{0, 0}, {2, 0}, {2, 3}, {0, 3}, {0, 0}}); a != 6 {
		t.Errorf("ringArea = %v, want 6", a)
	}
}

func TestOuterRingEmpty(t *testing.T) {
	for _, tc := range []struct{ typ, coords string }{{"", `null`}, {"Polygon", `[]`}, {"MultiPolygon", `[[]]`}} {
		if ring, err := outerRing(tc.typ, json.RawMessage(tc.coords)); err != nil || ring != nil {
			t.Errorf("outerRing(%q, %s) = %v, %v, want nil", tc.typ, tc.coords, ring, err)
		}
	}
}
