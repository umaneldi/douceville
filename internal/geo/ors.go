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

const orsBaseURL = "https://api.openrouteservice.org/v2/isochrones/"

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
			Value float64 `json:"value"`
		} `json:"properties"`
		Geometry struct {
			Coordinates [][][2]float64 `json:"coordinates"`
		} `json:"geometry"`
	} `json:"features"`
}

// LevelInterval est le pas (en secondes) des isochrones emboîtées pour un
// temps maximal dist : 5 min, ou 10 min au-delà de 50 min (ORS limite une
// requête à 10 isochrones).
func LevelInterval(dist float64) float64 {
	if dist > 3000 {
		return 600
	}
	return 300
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

	payload := map[string]any{
		"locations": [][2]float64{lonlat},
		"range":     []float64{dist},
		"interval":  interval,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return models.Isochrone{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, orsBaseURL+transp, bytes.NewReader(body))
	if err != nil {
		return models.Isochrone{}, err
	}
	req.Header.Set("Authorization", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	// ORS renvoie du GeoJSON et répond 406 si l'on n'accepte que application/json.
	req.Header.Set("Accept", "application/geo+json, application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return models.Isochrone{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return models.Isochrone{}, fmt.Errorf("openrouteservice returned status %d", resp.StatusCode)
	}

	var parsed orsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
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
