package geo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// BANResult est le point géocodé renvoyé par addok.
type BANResult struct {
	Longitude float64
	Latitude  float64
}

// BANClient interroge une instance addok (Base Adresse Nationale).
type BANClient struct {
	host string
	http *http.Client
}

// NewBANClient crée un client addok ciblant http://<host>:7878.
func NewBANClient(host string, httpClient *http.Client) *BANClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &BANClient{host: host, http: httpClient}
}

type banResponse struct {
	Features []struct {
		Geometry struct {
			Coordinates [2]float64 `json:"coordinates"`
		} `json:"geometry"`
	} `json:"features"`
}

// Geocode résout une adresse libre en coordonnées [lon, lat].
func (c *BANClient) Geocode(ctx context.Context, query string) (BANResult, error) {
	endpoint := fmt.Sprintf("http://%s:7878/search?q=%s", c.host, url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return BANResult{}, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return BANResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return BANResult{}, fmt.Errorf("addok returned status %d", resp.StatusCode)
	}

	var parsed banResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return BANResult{}, err
	}
	if len(parsed.Features) == 0 {
		return BANResult{}, fmt.Errorf("addok found no result for %q", query)
	}

	coords := parsed.Features[0].Geometry.Coordinates
	return BANResult{Longitude: coords[0], Latitude: coords[1]}, nil
}
