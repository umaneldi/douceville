package api

import (
	"encoding/json"
	"html"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/umaneldi/douceville/internal/auth"
	"github.com/umaneldi/douceville/web"
)

// NewRouter construit le routeur de l'application monolithique : l'API JSON est
// montée sous /api, et l'interface web statique (embarquée) est servie à la racine.
func NewRouter(jwtSecret string, h *Handlers, users auth.UserLookup) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(requestLogger)
	r.Use(middleware.Recoverer)

	r.Route("/api", func(api chi.Router) {
		// Routes publiques (authentification).
		api.Post("/auth/register", h.register)
		api.Post("/auth/login", h.login)
		api.Post("/auth/verify", h.verifyEmail)
		api.Post("/auth/verify/info", h.verifyInfo)
		api.Post("/auth/resend-verification", h.resendVerification)
		api.Get("/auth/oidc/config", h.oidcConfig)
		api.Get("/auth/oidc/login", h.oidcLogin)
		api.Get("/auth/oidc/callback", h.oidcCallback)
		api.Post("/billing/webhook/mollie", h.mollieWebhook)

		// Routes protégées par un token Bearer.
		api.Group(func(protected chi.Router) {
			protected.Use(auth.Middleware(jwtSecret, users))
			protected.Get("/etablissement/{uai}", h.readEtablissement)
			protected.Post("/etablissements", h.etablissementsInZone)
			protected.Get("/isochrone", h.isochrone)
			protected.Get("/address/suggest", h.addressSuggest)
			protected.Get("/user", h.getUser)
			protected.Patch("/user", h.updateProfile)
			protected.Post("/user/email", h.changeEmail)
			protected.Post("/user/password", h.changePassword)
			protected.Post("/auth/logout", h.logout)
			protected.Get("/billing", h.getBilling)
			protected.Post("/billing/topup", h.topup)
			protected.Post("/billing/sync", h.syncPayments)

			// Administration des comptes (niveau 'admin').
			protected.Route("/admin", func(admin chi.Router) {
				admin.Use(auth.RequireAdmin)
				admin.Get("/users", h.listUsers)
				admin.Patch("/users/{id}", h.setUserPerms)
				admin.Delete("/users/{id}", h.deleteUser)
				admin.Post("/users/{id}/credit", h.adjustCredit)
				admin.Get("/users/{id}/transactions", h.listUserTransactions)
				admin.Get("/billing/settings", h.getBillingSettings)
				admin.Put("/billing/settings", h.updateBillingSettings)
			})
		})
	})

	r.Handle("/*", spaHandler())

	return r
}

// spaHandler sert les fichiers statiques embarqués ; toute route inconnue
// retombe sur index.html (application monopage).
func spaHandler() http.HandlerFunc {
	fileServer := http.FileServerFS(web.Files)
	index := renderIndex()

	return func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" || p == "index.html" {
			serveIndex(w, index)
			return
		}
		if _, err := fs.Stat(web.Files, p); err != nil {
			// Fichier inconnu : on sert l'application monopage.
			serveIndex(w, index)
			return
		}
		fileServer.ServeHTTP(w, r)
	}
}

// renderIndex lit index.html et y injecte la version de l'application.
func renderIndex() []byte {
	b, err := fs.ReadFile(web.Files, "index.html")
	if err != nil {
		panic("index.html embarqué introuvable : " + err.Error())
	}
	v := html.EscapeString(web.Version)
	return []byte(strings.ReplaceAll(string(b), "{{VERSION}}", v))
}

func serveIndex(w http.ResponseWriter, index []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(index)
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
