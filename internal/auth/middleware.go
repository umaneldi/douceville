package auth

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/umaneldi/douceville/internal/models"
)

// UserLookup récupère le profil d'un utilisateur par son identifiant (claim sub)
// et indique si un token a été révoqué (déconnexion).
type UserLookup interface {
	// GetUser renvoie (nil, nil) si aucun utilisateur ne correspond.
	GetUser(ctx context.Context, id string) (*models.DvUser, error)
	// IsTokenRevoked indique si le token d'identifiant jti a été révoqué.
	IsTokenRevoked(ctx context.Context, jti string) (bool, error)
}

// CodeAccountNotGranted est le code d'erreur renvoyé quand un compte n'a pas
// (ou plus) accès à l'application.
const CodeAccountNotGranted = "account_not_granted"

type ctxKey struct{}

type claimsCtxKey struct{}

// Middleware construit un middleware HTTP qui exige un token Bearer valide.
//
// Codes d'erreur (parité avec le backend Python) :
//   - 403 si le header Authorization est absent ou si le schéma n'est pas Bearer ;
//   - 401 si le token est invalide/expiré/révoqué ou si l'utilisateur est inconnu.
//   - 403 (code "account_not_granted") si le compte n'a pas au moins le niveau
//     'granted' (niveau relu en base à chaque requête : un retrait d'accès est
//     immédiat).
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

			revoked, err := users.IsTokenRevoked(r.Context(), claims.ID)
			if err != nil {
				logger(r).Error("auth: revocation lookup failed", "user_id", claims.Sub, "error", err)
				writeError(w, http.StatusInternalServerError, "token lookup failed")
				return
			}
			if revoked {
				logger(r).Warn("auth: revoked token", "user_id", claims.Sub)
				writeError(w, http.StatusUnauthorized, "token has been revoked")
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

			if !user.Perms.AtLeast(models.PermsGranted) {
				logger(r).Warn("auth: account not granted", "user_id", user.ID, "perms", user.Perms)
				writeJSON(w, http.StatusForbidden, map[string]string{
					"detail": "account not granted",
					"code":   CodeAccountNotGranted,
				})
				return
			}

			// Le login est l'e-mail courant du compte (il a pu changer depuis
			// l'émission du token), à défaut le claim email du token.
			user.Login = claims.Email
			if user.Email != nil && *user.Email != "" {
				user.Login = *user.Email
			}

			logger(r).Debug("auth: request authenticated", "user_id", user.ID)

			ctx := context.WithValue(r.Context(), ctxKey{}, user)
			ctx = context.WithValue(ctx, claimsCtxKey{}, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// CodeAdminRequired est le code d'erreur renvoyé quand une route
// d'administration est appelée par un compte non administrateur.
const CodeAdminRequired = "admin_required"

// RequireAdmin restreint une route aux comptes 'admin'. Il doit être placé
// après Middleware (qui attache l'utilisateur, relu en base à chaque requête).
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := UserFromContext(r.Context())
		if !ok || !user.Perms.AtLeast(models.PermsAdmin) {
			if ok {
				logger(r).Warn("auth: admin required", "user_id", user.ID, "perms", user.Perms)
			}
			writeJSON(w, http.StatusForbidden, map[string]string{
				"detail": "admin required",
				"code":   CodeAdminRequired,
			})
			return
		}
		next.ServeHTTP(w, r)
	})
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

// ClaimsFromContext renvoie les claims du token ayant authentifié la requête.
func ClaimsFromContext(ctx context.Context) (Claims, bool) {
	c, ok := ctx.Value(claimsCtxKey{}).(Claims)
	return c, ok
}

func writeError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]string{"detail": detail})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
