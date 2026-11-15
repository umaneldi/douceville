package api

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/ydethe/douceville/internal/auth"
	"github.com/ydethe/douceville/web"
)

// NewRouter construit le routeur de l'application monolithique : l'API JSON est
// montée sous /api, et l'interface web statique (embarquée) est servie à la racine.
func NewRouter(jwtSecret string, h *Handlers, users auth.UserLookup) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)

	r.Route("/api", func(api chi.Router) {
		// Routes publiques (authentification).
		api.Post("/auth/register", h.register)
		api.Post("/auth/login", h.login)

		// Routes protégées par un token Bearer.
		api.Group(func(protected chi.Router) {
			protected.Use(auth.Middleware(jwtSecret, users))
			protected.Get("/etablissement/{uai}", h.readEtablissement)
			protected.Post("/etablissements", h.etablissementsInZone)
			protected.Get("/isochrone", h.isochrone)
			protected.Get("/user", h.getUser)
		})
	})

	r.Handle("/*", spaHandler())

	return r
}

// spaHandler sert les fichiers statiques embarqués ; toute route inconnue
// retombe sur index.html (application monopage).
func spaHandler() http.HandlerFunc {
	fileServer := http.FileServerFS(web.Files)

	return func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(web.Files, p); err != nil {
			// Fichier inconnu : on sert l'application monopage.
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	}
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
