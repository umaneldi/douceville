// Package geo regroupe les clients des services de géolocalisation externes :
// OpenRouteService (isochrones) et addok/BAN (géocodage d'adresses).
package geo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ydethe/douceville/internal/models"
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
		Geometry struct {
			Coordinates [][][2]float64 `json:"coordinates"`
		} `json:"geometry"`
	} `json:"features"`
}

// CalcIsochrone calcule l'isochrone centrée sur lonlat ([lon, lat]) pour une
// distance (en secondes) et un profil de transport donnés.
func (c *ORSClient) CalcIsochrone(ctx context.Context, lonlat [2]float64, dist float64, transp string) (models.Isochrone, error) {
	if transp == "" {
		transp = "driving-car"
	}

	payload := map[string]any{
		"locations": [][2]float64{lonlat},
		"range":     []float64{dist},
		"interval":  dist,
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
	req.Header.Set("Accept", "application/json")

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
	if len(parsed.Features) == 0 || len(parsed.Features[0].Geometry.Coordinates) == 0 {
		return models.Isochrone{}, fmt.Errorf("openrouteservice returned no isochrone geometry")
	}

	return models.Isochrone{
		LonLat:   lonlat,
		Dist:     dist,
		Transp:   transp,
		Geometry: parsed.Features[0].Geometry.Coordinates[0],
	}, nil
}

// PolygonWKT construit la représentation WKT POLYGON((...)) de l'anneau de
// l'isochrone, utilisable avec ST_GeomFromEWKT côté PostGIS.
func PolygonWKT(iso models.Isochrone) string {
	var b bytes.Buffer
	b.WriteString("POLYGON((")
	for i, pt := range iso.Geometry {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%f %f", pt[0], pt[1])
	}
	b.WriteString("))")
	return b.String()
}
