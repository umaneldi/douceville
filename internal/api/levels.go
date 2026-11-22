package api

import (
	"encoding/json"
	"sort"

	"github.com/umaneldi/douceville/internal/geo"
	"github.com/umaneldi/douceville/internal/models"
)

// levelWKTs est un niveau de la zone de recherche avant intersection : le
// temps de trajet (secondes) et, pour chaque point de départ, le WKT de
// l'isochrone à intersecter.
type levelWKTs struct {
	Dist float64
	WKTs []string
}

// isoLevels renvoie les niveaux d'une isochrone ; une isochrone sans niveaux
// en a un seul, son polygone de temps maximal.
func isoLevels(iso models.Isochrone) []models.IsoLevel {
	if len(iso.Levels) > 0 {
		return iso.Levels
	}
	return []models.IsoLevel{{Dist: iso.Dist, Geometry: iso.Geometry}}
}

// zoneLevelWKTs construit les niveaux de la zone de recherche, par temps
// croissant. Les temps sont l'union de ceux des niveaux de chaque isochrone ;
// pour un temps t, chaque point de départ contribue son plus grand niveau
// <= t (au-delà de son temps maximal, son isochrone maximale). Un temps pour
// lequel un point de départ n'a aucun niveau <= t est ignoré. Le dernier
// niveau est l'intersection des isochrones de temps maximal.
func zoneLevelWKTs(isos []models.Isochrone) []levelWKTs {
	seen := map[float64]bool{}
	var dists []float64
	for _, iso := range isos {
		for _, lv := range isoLevels(iso) {
			if !seen[lv.Dist] {
				seen[lv.Dist] = true
				dists = append(dists, lv.Dist)
			}
		}
	}
	sort.Float64s(dists)

	var out []levelWKTs
	for _, t := range dists {
		wkts := make([]string, 0, len(isos))
		for _, iso := range isos {
			var best *models.IsoLevel
			for _, lv := range isoLevels(iso) {
				if lv.Dist <= t && (best == nil || lv.Dist > best.Dist) {
					best = &lv
				}
			}
			if best == nil {
				break
			}
			wkts = append(wkts, geo.RingWKT(best.Geometry))
		}
		if len(wkts) == len(isos) {
			out = append(out, levelWKTs{Dist: t, WKTs: wkts})
		}
	}
	return out
}

// pointLonLat extrait [lon, lat] d'un Point GeoJSON.
func pointLonLat(raw json.RawMessage) ([2]float64, bool) {
	var p struct {
		Type        string     `json:"type"`
		Coordinates [2]float64 `json:"coordinates"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &p) != nil || p.Type != "Point" {
		return [2]float64{}, false
	}
	return p.Coordinates, true
}
