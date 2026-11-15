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
