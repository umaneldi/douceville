package models

import (
	"encoding/json"
	"testing"
)

// Vérifie que les noms de champs JSON reproduisent ceux du backend Python.
func TestEtablissementJSONFields(t *testing.T) {
	e := Etablissement{
		UAI:        "X42Y",
		Nom:        "Test",
		CodePostal: "31000",
		Position:   json.RawMessage(`{"type":"Point","coordinates":[1.44,43.6]}`),
		Resultats:  []Resultat{},
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, key := range []string{"UAI", "code_postal", "position", "resultats", "lieu_dit"} {
		if _, ok := m[key]; !ok {
			t.Errorf("champ JSON %q absent", key)
		}
	}
	// position doit être transmise telle quelle (GeoJSON), pas ré-encodée en chaîne.
	if string(m["position"]) != `{"type":"Point","coordinates":[1.44,43.6]}` {
		t.Errorf("position mal sérialisée: %s", m["position"])
	}
}

func TestIsochroneJSONFields(t *testing.T) {
	iso := Isochrone{
		LonLat:   [2]float64{1.44, 43.6},
		Dist:     600,
		Transp:   "driving-car",
		Geometry: [][2]float64{{1, 2}, {3, 4}},
	}
	b, err := json.Marshal(iso)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"lonlat", "dist", "transp", "geometry"} {
		if _, ok := m[key]; !ok {
			t.Errorf("champ JSON %q absent", key)
		}
	}
}

func TestPermsAtLeast(t *testing.T) {
	order := []Perms{PermsUnverified, PermsVerified, PermsGranted, PermsAdmin}
	for i, p := range order {
		for j, min := range order {
			if got, want := p.AtLeast(min), i >= j; got != want {
				t.Errorf("%s.AtLeast(%s) = %v, want %v", p, min, got, want)
			}
		}
	}
	if Perms("bogus").AtLeast(PermsUnverified) {
		t.Error("unknown perms must not reach any level")
	}
}

func TestPermsValid(t *testing.T) {
	for _, p := range []Perms{PermsUnverified, PermsVerified, PermsGranted, PermsAdmin} {
		if !p.Valid() {
			t.Errorf("%q should be valid", p)
		}
	}
	for _, p := range []Perms{"", "root", "Admin"} {
		if p.Valid() {
			t.Errorf("%q should not be valid", p)
		}
	}
}

func TestFiltresZoneActif(t *testing.T) {
	v := 1.0
	for _, tc := range []struct {
		f    FiltresZone
		want bool
	}{
		{FiltresZone{}, false},
		{FiltresZone{PrixM2Min: &v}, true},
		{FiltresZone{PrixM2Max: &v}, true},
		{FiltresZone{SecuriteMin: &v}, true},
		{FiltresZone{FiscaliteMax: &v}, true},
	} {
		if got := tc.f.Actif(); got != tc.want {
			t.Errorf("Actif(%+v) = %v, want %v", tc.f, got, tc.want)
		}
	}
}

func TestSourceLonLat(t *testing.T) {
	for _, tc := range []struct {
		pos  string
		want [2]float64
		ok   bool
	}{
		{`{"type":"Point","coordinates":[1.44,43.6]}`, [2]float64{1.44, 43.6}, true},
		{`{"type":"LineString","coordinates":[[1,2],[3,4]]}`, [2]float64{}, false},
		{`{`, [2]float64{}, false},
		{``, [2]float64{}, false},
	} {
		s := Source{Etablissement: Etablissement{Position: json.RawMessage(tc.pos)}}
		if got, ok := s.LonLat(); got != tc.want || ok != tc.ok {
			t.Errorf("LonLat(%s) = %v, %v; want %v, %v", tc.pos, got, ok, tc.want, tc.ok)
		}
	}
}

func TestNewIsochroneKey(t *testing.T) {
	k := NewIsochroneKey([2]float64{1.444446, -43.600004}, 599.6, "foot-walking")
	if k != (IsochroneKey{LonE5: 144445, LatE5: -4360000, Dist: 600, Transp: "foot-walking"}) {
		t.Errorf("key = %+v", k)
	}
	// Deux positions à moins d'un demi-mètre partagent la clé.
	if NewIsochroneKey([2]float64{1.400001, 43.6}, 600, "x") != NewIsochroneKey([2]float64{1.4, 43.600002}, 600, "x") {
		t.Error("close positions must share the key")
	}
}
