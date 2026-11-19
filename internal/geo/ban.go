package geo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// BANResult est le point géocodé renvoyé par addok.
type BANResult struct {
	Longitude float64
	Latitude  float64
}

// BANClient interroge une instance addok (Base Adresse Nationale).
type BANClient struct {
	baseURL string
	http    *http.Client
}

// PublicBANURL est l'API publique de géocodage BAN (Géoplateforme IGN, addok),
// qui remplace api-adresse.data.gouv.fr.
const PublicBANURL = "https://data.geopf.fr/geocodage"

// NewBANClient crée un client addok ciblant http://<host>:7878, ou l'API
// publique de la BAN si host est vide.
func NewBANClient(host string, httpClient *http.Client) *BANClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	baseURL := PublicBANURL
	if host != "" {
		baseURL = fmt.Sprintf("http://%s:7878", host)
	}
	return &BANClient{baseURL: baseURL, http: httpClient}
}

type banResponse struct {
	Features []struct {
		Properties struct {
			Label string `json:"label"`
		} `json:"properties"`
		Geometry struct {
			Coordinates [2]float64 `json:"coordinates"`
		} `json:"geometry"`
	} `json:"features"`
}

// Geocode résout une adresse libre en coordonnées [lon, lat].
func (c *BANClient) Geocode(ctx context.Context, query string) (BANResult, error) {
	parsed, err := c.search(ctx, query, 1, false)
	if err != nil {
		return BANResult{}, err
	}
	if len(parsed.Features) == 0 {
		return BANResult{}, fmt.Errorf("addok found no result for %q", query)
	}

	coords := parsed.Features[0].Geometry.Coordinates
	return BANResult{Longitude: coords[0], Latitude: coords[1]}, nil
}

// BANSuggestion est une proposition d'adresse pour l'autocomplétion.
type BANSuggestion struct {
	Label     string  `json:"label"`
	Longitude float64 `json:"lon"`
	Latitude  float64 `json:"lat"`
}

// Suggest renvoie jusqu'à limit adresses correspondant au début de saisie query
// (mode autocomplete d'addok).
func (c *BANClient) Suggest(ctx context.Context, query string, limit int) ([]BANSuggestion, error) {
	parsed, err := c.search(ctx, query, limit, true)
	if err != nil {
		return nil, err
	}
	out := make([]BANSuggestion, 0, len(parsed.Features))
	for _, f := range parsed.Features {
		out = append(out, BANSuggestion{
			Label:     f.Properties.Label,
			Longitude: f.Geometry.Coordinates[0],
			Latitude:  f.Geometry.Coordinates[1],
		})
	}
	return out, nil
}

func (c *BANClient) search(ctx context.Context, query string, limit int, autocomplete bool) (banResponse, error) {
	params := url.Values{}
	params.Set("q", query)
	params.Set("limit", strconv.Itoa(limit))
	if autocomplete {
		params.Set("autocomplete", "1")
	}
	endpoint := c.baseURL + "/search?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return banResponse{}, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return banResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return banResponse{}, fmt.Errorf("addok returned status %d", resp.StatusCode)
	}

	var parsed banResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return banResponse{}, err
	}
	return parsed, nil
}
