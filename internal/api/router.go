package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/ydethe/douceville/internal/auth"
)

// NewRouter construit le routeur chi. Toutes les routes sont montées sous apiPath
// (équivalent du root_path FastAPI) et protégées par le middleware d'auth Bearer.
func NewRouter(apiPath, jwtSecret string, h *Handlers, users auth.UserLookup) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)

	authenticated := chi.NewRouter()
	authenticated.Use(auth.Middleware(jwtSecret, users))

	authenticated.Get("/etablissement/{uai}", h.readEtablissement)
	authenticated.Post("/etablissements", h.etablissementsInZone)
	authenticated.Get("/isochrone", h.isochrone)
	authenticated.Get("/user", h.getUser)

	if apiPath == "" {
		r.Mount("/", authenticated)
	} else {
		r.Mount(apiPath, authenticated)
	}

	return r
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]string{"detail": detail})
}

func parseFloat(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}
