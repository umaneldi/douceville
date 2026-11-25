// Package geo regroupe les clients des services de géolocalisation externes :
// OpenRouteService (isochrones) et addok/BAN (géocodage d'adresses).
package geo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"github.com/umaneldi/douceville/internal/models"
)

// orsBaseURL est le point d'accès des isochrones : api.openrouteservice.org
// répond désormais 410 Gone, l'API est servie par api.heigit.org.
const orsBaseURL = "https://api.heigit.org/openrouteservice/v2/isochrones/"

// ORSClient interroge l'API OpenRouteService.
type ORSClient struct {
	apiKey string
	http   *http.Client
}

// NewORSClient crée un client ORS.
func NewORSClient(apiKey string, httpClient *http.Client) *ORSClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &ORSClient{apiKey: apiKey, http: httpClient}
}

type orsResponse struct {
	Features []struct {
		Properties struct {
			Value      float64 `json:"value"`
			GroupIndex int     `json:"group_index"`
		} `json:"properties"`
		Geometry struct {
			Coordinates [][][2]float64 `json:"coordinates"`
		} `json:"geometry"`
	} `json:"features"`
}

// CalcIsochrone calcule les isochrones centrées sur lonlat ([lon, lat]) pour
// un temps maximal dist et un pas interval (en secondes ; un seul niveau si
// interval <= 0) et un profil de transport donnés.
func (c *ORSClient) CalcIsochrone(ctx context.Context, lonlat [2]float64, dist, interval float64, transp string) (models.Isochrone, error) {
	if transp == "" {
		transp = "driving-car"
	}
	if interval <= 0 || interval > dist {
		interval = dist
	}

	parsed, err := c.post(ctx, transp, map[string]any{
		"locations": [][2]float64{lonlat},
		"range":     []float64{dist},
		"interval":  interval,
	})
	if err != nil {
		return models.Isochrone{}, err
	}
	var levels []models.IsoLevel
	for _, f := range parsed.Features {
		if len(f.Geometry.Coordinates) == 0 {
			continue
		}
		levels = append(levels, models.IsoLevel{Dist: f.Properties.Value, Geometry: f.Geometry.Coordinates[0]})
	}
	if len(levels) == 0 {
		return models.Isochrone{}, fmt.Errorf("openrouteservice returned no isochrone geometry")
	}
	sort.Slice(levels, func(i, j int) bool { return levels[i].Dist < levels[j].Dist })

	return models.Isochrone{
		LonLat:   lonlat,
		Dist:     dist,
		Transp:   transp,
		Geometry: levels[len(levels)-1].Geometry,
		Levels:   levels,
	}, nil
}

// orsMaxLocations est le nombre maximal de positions par requête
// d'isochrones acceptées par OpenRouteService.
const orsMaxLocations = 5

// CalcIsochrones calcule l'isochrone de temps dist (secondes, un seul niveau)
// de chaque position [lon, lat] pour un profil de transport, par requêtes de
// orsMaxLocations positions. Le résultat suit l'ordre de lonlats ; une
// position sans géométrie dans la réponse est une erreur.
func (c *ORSClient) CalcIsochrones(ctx context.Context, lonlats [][2]float64, dist float64, transp string) ([]models.Isochrone, error) {
	if transp == "" {
		transp = "driving-car"
	}
	out := make([]models.Isochrone, len(lonlats))
	for start := 0; start < len(lonlats); start += orsMaxLocations {
		batch := lonlats[start:min(start+orsMaxLocations, len(lonlats))]
		parsed, err := c.post(ctx, transp, map[string]any{"locations": batch, "range": []float64{dist}})
		if err != nil {
			return nil, err
		}
		found := make([]bool, len(batch))
		for _, f := range parsed.Features {
			g := f.Properties.GroupIndex
			if g < 0 || g >= len(batch) || len(f.Geometry.Coordinates) == 0 {
				continue
			}
			found[g] = true
			out[start+g] = models.Isochrone{LonLat: batch[g], Dist: dist, Transp: transp, Geometry: f.Geometry.Coordinates[0]}
		}
		for i, ok := range found {
			if !ok {
				return nil, fmt.Errorf("openrouteservice returned no isochrone geometry for location %d", start+i)
			}
		}
	}
	return out, nil
}

// post envoie une requête d'isochrones pour le profil transp et décode la réponse.
func (c *ORSClient) post(ctx context.Context, transp string, payload map[string]any) (orsResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return orsResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, orsBaseURL+transp, bytes.NewReader(body))
	if err != nil {
		return orsResponse{}, err
	}
	req.Header.Set("Authorization", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	// ORS renvoie du GeoJSON et répond 406 si l'on n'accepte que application/json.
	req.Header.Set("Accept", "application/geo+json, application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return orsResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return orsResponse{}, fmt.Errorf("openrouteservice returned status %d", resp.StatusCode)
	}
	var parsed orsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return orsResponse{}, err
	}
	return parsed, nil
}

// PolygonWKT construit la représentation WKT POLYGON((...)) de l'anneau de
// l'isochrone, utilisable avec ST_GeomFromEWKT côté PostGIS.
func PolygonWKT(iso models.Isochrone) string {
	return RingWKT(iso.Geometry)
}

// RingWKT construit la représentation WKT POLYGON((...)) d'un anneau.
func RingWKT(ring [][2]float64) string {
	var b bytes.Buffer
	b.WriteString("POLYGON((")
	for i, pt := range ring {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%f %f", pt[0], pt[1])
	}
	b.WriteString("))")
	return b.String()
}
