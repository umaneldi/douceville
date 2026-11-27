package geo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"

	"github.com/umaneldi/douceville/internal/models"
	"golang.org/x/sync/errgroup"
)

// DefaultValhallaConcurrency est le nombre de requêtes d'isochrones envoyées
// en parallèle à Valhalla si NewValhallaClient reçoit une valeur <= 0.
const DefaultValhallaConcurrency = 8

// valhallaGeneralize est la tolérance (mètres) de simplification
// Douglas-Peucker des isochrones Valhalla : des anneaux plus légers à stocker
// en cache et à intersecter, sans perte visible à l'échelle d'une commune.
const valhallaGeneralize = 20

// valhallaCosting traduit les profils de transport publics de l'API
// (models.Transports, noms OpenRouteService) en modèles de coût Valhalla.
var valhallaCosting = map[string]string{
	"driving-car":     "auto",
	"cycling-regular": "bicycle",
	"foot-walking":    "pedestrian",
}

// ValhallaClient interroge un service Valhalla auto-hébergé (isochrones et
// itinéraires). Il implémente les mêmes interfaces que ORSClient.
type ValhallaClient struct {
	baseURL     string
	http        *http.Client
	concurrency int
}

// NewValhallaClient crée un client pour le service Valhalla de baseURL
// (ex. http://valhalla:8002). concurrency borne le nombre de requêtes
// d'isochrones simultanées (DefaultValhallaConcurrency si <= 0).
func NewValhallaClient(baseURL string, concurrency int, httpClient *http.Client) *ValhallaClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if concurrency <= 0 {
		concurrency = DefaultValhallaConcurrency
	}
	return &ValhallaClient{baseURL: strings.TrimRight(baseURL, "/"), http: httpClient, concurrency: concurrency}
}

// costing renvoie le modèle de coût Valhalla d'un profil (auto par défaut).
func costing(transp string) string {
	if c, ok := valhallaCosting[transp]; ok {
		return c
	}
	return "auto"
}

// valhallaLocation est une position au format Valhalla.
type valhallaLocation struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

func location(lonlat [2]float64) valhallaLocation {
	return valhallaLocation{Lat: lonlat[1], Lon: lonlat[0]}
}

// valhallaIsochrones est la réponse GeoJSON de /isochrone : un polygone (ou
// multipolygone) par contour, de temps properties.contour en minutes.
type valhallaIsochrones struct {
	Features []struct {
		Properties struct {
			Contour float64 `json:"contour"`
		} `json:"properties"`
		Geometry struct {
			Type        string          `json:"type"`
			Coordinates json.RawMessage `json:"coordinates"`
		} `json:"geometry"`
	} `json:"features"`
}

// isochrone demande les contours times (secondes) autour de lonlat et renvoie
// un niveau par contour, triés par temps croissant.
func (c *ValhallaClient) isochrone(ctx context.Context, lonlat [2]float64, times []float64, transp string) ([]models.IsoLevel, error) {
	contours := make([]map[string]float64, len(times))
	for i, t := range times {
		contours[i] = map[string]float64{"time": t / 60}
	}
	var parsed valhallaIsochrones
	if err := c.do(ctx, "/isochrone", map[string]any{
		"locations":  []valhallaLocation{location(lonlat)},
		"costing":    costing(transp),
		"contours":   contours,
		"polygons":   true,
		"denoise":    1,
		"generalize": valhallaGeneralize,
	}, &parsed); err != nil {
		return nil, err
	}
	var levels []models.IsoLevel
	for _, f := range parsed.Features {
		ring, err := outerRing(f.Geometry.Type, f.Geometry.Coordinates)
		if err != nil {
			return nil, err
		}
		if len(ring) == 0 {
			continue
		}
		levels = append(levels, models.IsoLevel{Dist: f.Properties.Contour * 60, Geometry: ring})
	}
	if len(levels) == 0 {
		return nil, fmt.Errorf("valhalla returned no isochrone geometry")
	}
	sort.Slice(levels, func(i, j int) bool { return levels[i].Dist < levels[j].Dist })
	return levels, nil
}

// outerRing extrait l'anneau extérieur d'un Polygon, ou celui du polygone de
// plus grande aire d'un MultiPolygon ; nil pour une géométrie vide.
func outerRing(typ string, coords json.RawMessage) ([][2]float64, error) {
	switch typ {
	case "Polygon":
		var poly [][][2]float64
		if err := json.Unmarshal(coords, &poly); err != nil {
			return nil, err
		}
		if len(poly) == 0 {
			return nil, nil
		}
		return poly[0], nil
	case "MultiPolygon":
		var multi [][][][2]float64
		if err := json.Unmarshal(coords, &multi); err != nil {
			return nil, err
		}
		var best [][2]float64
		bestArea := -1.0
		for _, poly := range multi {
			if len(poly) == 0 {
				continue
			}
			if a := ringArea(poly[0]); a > bestArea {
				best, bestArea = poly[0], a
			}
		}
		return best, nil
	case "":
		return nil, nil
	default:
		return nil, fmt.Errorf("valhalla returned unexpected geometry type %q", typ)
	}
}

// ringArea est l'aire (en degrés², valeur absolue) d'un anneau, par la formule
// du lacet : suffisante pour comparer des polygones voisins.
func ringArea(ring [][2]float64) float64 {
	var a float64
	for i := range ring {
		j := (i + 1) % len(ring)
		a += ring[i][0]*ring[j][1] - ring[j][0]*ring[i][1]
	}
	return math.Abs(a) / 2
}

// CalcIsochrone calcule les isochrones centrées sur lonlat ([lon, lat]) pour
// un temps maximal dist et un pas interval (en secondes ; un seul niveau si
// interval <= 0 ou > dist) et un profil de transport donnés.
func (c *ValhallaClient) CalcIsochrone(ctx context.Context, lonlat [2]float64, dist, interval float64, transp string) (models.Isochrone, error) {
	if transp == "" {
		transp = "driving-car"
	}
	if interval <= 0 || interval > dist {
		interval = dist
	}
	var times []float64
	for t := interval; t < dist; t += interval {
		times = append(times, t)
	}
	times = append(times, dist)

	levels, err := c.isochrone(ctx, lonlat, times, transp)
	if err != nil {
		return models.Isochrone{}, err
	}
	return models.Isochrone{
		LonLat:   lonlat,
		Dist:     dist,
		Transp:   transp,
		Geometry: levels[len(levels)-1].Geometry,
		Levels:   levels,
	}, nil
}

// CalcIsochrones calcule l'isochrone de temps dist (secondes, un seul niveau)
// de chaque position [lon, lat] : Valhalla fusionne les positions d'une même
// requête, d'où une requête par position, au plus c.concurrency à la fois.
// Le résultat suit l'ordre de lonlats ; un échec est une erreur de l'ensemble.
func (c *ValhallaClient) CalcIsochrones(ctx context.Context, lonlats [][2]float64, dist float64, transp string) ([]models.Isochrone, error) {
	if transp == "" {
		transp = "driving-car"
	}
	out := make([]models.Isochrone, len(lonlats))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(c.concurrency)
	for i, ll := range lonlats {
		g.Go(func() error {
			levels, err := c.isochrone(gctx, ll, []float64{dist}, transp)
			if err != nil {
				return fmt.Errorf("location %d: %w", i, err)
			}
			out[i] = models.Isochrone{LonLat: ll, Dist: dist, Transp: transp, Geometry: levels[len(levels)-1].Geometry}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}

// valhallaRoute est la réponse de /route (directions_type none) : longueur en
// km, durée en secondes et tracé encodé (polyligne, précision 1e-6) par
// tronçon.
type valhallaRoute struct {
	Trip struct {
		Legs []struct {
			Shape string `json:"shape"`
		} `json:"legs"`
		Summary struct {
			Length float64 `json:"length"`
			Time   float64 `json:"time"`
		} `json:"summary"`
	} `json:"trip"`
}

// CalcItineraire calcule l'itinéraire de from à to ([lon, lat]) pour un
// profil de transport (driving-car par défaut).
func (c *ValhallaClient) CalcItineraire(ctx context.Context, from, to [2]float64, transp string) (models.Itineraire, error) {
	var parsed valhallaRoute
	if err := c.do(ctx, "/route", map[string]any{
		"locations":       []valhallaLocation{location(from), location(to)},
		"costing":         costing(transp),
		"units":           "kilometers",
		"directions_type": "none",
	}, &parsed); err != nil {
		return models.Itineraire{}, err
	}
	var geom [][2]float64
	for _, leg := range parsed.Trip.Legs {
		pts, err := decodePolyline6(leg.Shape)
		if err != nil {
			return models.Itineraire{}, err
		}
		geom = append(geom, pts...)
	}
	if len(geom) == 0 {
		return models.Itineraire{}, fmt.Errorf("valhalla returned no route")
	}
	return models.Itineraire{
		Geometry:  geom,
		DistanceM: parsed.Trip.Summary.Length * 1000,
		DureeS:    parsed.Trip.Summary.Time,
	}, nil
}

// decodePolyline6 décode une polyligne encodée (algorithme Google, précision
// 1e-6, utilisé par Valhalla) en positions [lon, lat].
func decodePolyline6(s string) ([][2]float64, error) {
	var out [][2]float64
	var lat, lon int64
	for i := 0; i < len(s); {
		var deltas [2]int64
		for k := range deltas {
			var result int64
			var shift uint
			for {
				if i >= len(s) {
					return nil, fmt.Errorf("truncated polyline")
				}
				b := int64(s[i]) - 63
				i++
				if b < 0 || b > 63 {
					return nil, fmt.Errorf("invalid polyline character %q", s[i-1])
				}
				result |= (b & 0x1f) << shift
				shift += 5
				if b < 0x20 {
					break
				}
			}
			if result&1 != 0 {
				deltas[k] = ^(result >> 1)
			} else {
				deltas[k] = result >> 1
			}
		}
		lat += deltas[0]
		lon += deltas[1]
		out = append(out, [2]float64{float64(lon) / 1e6, float64(lat) / 1e6})
	}
	return out, nil
}

// valhallaError est le corps d'une réponse d'erreur de Valhalla.
type valhallaError struct {
	Error string `json:"error"`
}

// do envoie payload en JSON à c.baseURL+path et décode la réponse dans out.
func (c *ValhallaClient) do(ctx context.Context, path string, payload map[string]any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e valhallaError
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return fmt.Errorf("valhalla returned status %d: %s", resp.StatusCode, e.Error)
		}
		return fmt.Errorf("valhalla returned status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
