package auth

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/ydethe/douceville/internal/models"
)

// UserLookup récupère le profil d'un utilisateur par son identifiant (claim sub).
// Renvoie (nil, nil) si aucun utilisateur ne correspond.
type UserLookup interface {
	GetUser(ctx context.Context, id string) (*models.DvUser, error)
}

type ctxKey struct{}

// Middleware construit un middleware HTTP qui exige un token Bearer valide.
//
// Codes d'erreur (parité avec le backend Python) :
//   - 403 si le header Authorization est absent ou si le schéma n'est pas Bearer ;
//   - 401 si le token est invalide/expiré ou si l'utilisateur est inconnu.
func Middleware(secret string, users UserLookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				logger(r).Warn("auth: missing or malformed bearer token")
				writeError(w, http.StatusForbidden, "Not authenticated")
				return
			}

			claims, err := ParseToken(token, secret)
			if err != nil {
				logger(r).Warn("auth: invalid token", "error", err)
				writeError(w, http.StatusUnauthorized, err.Error())
				return
			}

			user, err := users.GetUser(r.Context(), claims.Sub)
			if err != nil {
				logger(r).Error("auth: user lookup failed", "user_id", claims.Sub, "error", err)
				writeError(w, http.StatusInternalServerError, "user lookup failed")
				return
			}
			if user == nil {
				logger(r).Warn("auth: token for unknown user", "user_id", claims.Sub)
				writeError(w, http.StatusUnauthorized, "No user found")
				return
			}

			// Le login provient du claim email du token (comme en Python).
			user.Login = claims.Email

			logger(r).Debug("auth: request authenticated", "user_id", user.ID)

			ctx := context.WithValue(r.Context(), ctxKey{}, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// logger renvoie le logger par défaut enrichi de l'identifiant de requête et du chemin.
func logger(r *http.Request) *slog.Logger {
	return slog.Default().With("request_id", middleware.GetReqID(r.Context()), "path", r.URL.Path)
}

// bearerToken extrait le token du header Authorization (schéma insensible à la casse).
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false
	}
	scheme, credentials, found := strings.Cut(h, " ")
	if !found || !strings.EqualFold(scheme, "bearer") || strings.TrimSpace(credentials) == "" {
		return "", false
	}
	return strings.TrimSpace(credentials), true
}

// UserFromContext renvoie l'utilisateur authentifié attaché à la requête.
func UserFromContext(ctx context.Context) (*models.DvUser, bool) {
	u, ok := ctx.Value(ctxKey{}).(*models.DvUser)
	return u, ok
}

func writeError(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"detail": detail})
}
