package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	netmail "net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/umaneldi/douceville/internal/auth"
	"github.com/umaneldi/douceville/internal/models"
	"github.com/umaneldi/douceville/internal/store"
)

// minPasswordLen est la longueur minimale acceptée pour un mot de passe.
const minPasswordLen = 8

// errCodeEmailNotVerified est le code renvoyé à la connexion d'un compte non vérifié.
const errCodeEmailNotVerified = "email_not_verified"

// validEmail vérifie qu'une adresse a la forme "local@domaine" (sans nom affiché).
func validEmail(email string) bool {
	if len(email) > 254 {
		return false
	}
	addr, err := netmail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return false
	}
	at := strings.LastIndex(email, "@")
	return at > 0 && strings.Contains(email[at+1:], ".")
}

// register gère POST /auth/register (inscription ouverte). Le compte est créé
// non vérifié et un lien de vérification est envoyé par e-mail ; aucun token
// n'est émis avant la vérification.
func (h *Handlers) register(w http.ResponseWriter, r *http.Request) {
	log := logger(r.Context())

	var body models.Credentials
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Warn("register: invalid JSON body", "error", err)
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	body.Email = strings.TrimSpace(strings.ToLower(body.Email))
	if !validEmail(body.Email) {
		log.Warn("register: invalid email", "email", body.Email)
		writeError(w, http.StatusUnprocessableEntity, "a valid email is required")
		return
	}
	if len(body.Password) < minPasswordLen {
		log.Warn("register: password too short", "email", body.Email)
		writeError(w, http.StatusUnprocessableEntity, "password must be at least 8 characters")
		return
	}

	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		log.Error("register: could not hash password", "email", body.Email, "error", err)
		writeError(w, http.StatusInternalServerError, "could not hash password")
		return
	}

	user, err := h.Store.CreateUser(r.Context(), body.Email, hash, body.FirstName, body.LastName)
	if err != nil {
		if errors.Is(err, store.ErrEmailTaken) {
			log.Warn("register: email already registered", "email", body.Email)
			writeError(w, http.StatusConflict, "email already registered")
			return
		}
		log.Error("register: could not create user", "email", body.Email, "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	log.Info("user registered", "user_id", user.ID, "email", body.Email)

	if err := h.sendVerification(r.Context(), user.ID, body.Email); err != nil {
		// Le compte existe : l'utilisateur pourra redemander un lien.
		log.Error("register: could not send verification e-mail", "user_id", user.ID, "error", err)
	}

	writeJSON(w, http.StatusCreated, map[string]string{
		"detail": "account created, check your e-mail to verify your address",
		"email":  body.Email,
	})
}

// resendVerification gère POST /auth/resend-verification. La réponse est
// toujours identique, pour ne pas révéler l'existence d'un compte.
func (h *Handlers) resendVerification(w http.ResponseWriter, r *http.Request) {
	log := logger(r.Context())

	var body models.EmailRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	email := strings.TrimSpace(strings.ToLower(body.Email))

	rec, err := h.Store.GetUserByEmail(r.Context(), email)
	if err != nil {
		log.Error("resend verification: user lookup failed", "email", email, "error", err)
		writeError(w, http.StatusInternalServerError, "user lookup failed")
		return
	}
	if rec != nil && rec.Perms == models.PermsUnverified {
		if err := h.sendVerification(r.Context(), rec.ID, email); err != nil {
			log.Error("resend verification: could not send e-mail", "user_id", rec.ID, "error", err)
		} else {
			log.Info("verification e-mail resent", "user_id", rec.ID)
		}
	} else {
		log.Info("resend verification: nothing to do", "email", email, "known", rec != nil)
	}

	writeJSON(w, http.StatusAccepted, map[string]string{
		"detail": "if an unverified account exists for this address, a new link has been sent",
	})
}

// verifyEmail gère POST /auth/verify : consomme le jeton reçu par e-mail et
// fait passer le compte à 'verified'. Un token n'est émis que si le compte a déjà
// accès (niveau 'granted' ou plus) ; sinon le compte attend la validation d'un
// administrateur.
func (h *Handlers) verifyEmail(w http.ResponseWriter, r *http.Request) {
	log := logger(r.Context())

	var body models.TokenRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Token == "" {
		writeError(w, http.StatusBadRequest, "a verification token is required")
		return
	}

	user, emailChanged, err := h.Store.VerifyEmail(r.Context(), auth.HashVerificationToken(body.Token))
	if errors.Is(err, store.ErrEmailTaken) {
		log.Warn("verify: new e-mail address already in use")
		writeError(w, http.StatusConflict, "this e-mail address is already in use")
		return
	}
	if err != nil {
		log.Error("verify: could not verify e-mail", "error", err)
		writeError(w, http.StatusInternalServerError, "could not verify e-mail")
		return
	}
	if user == nil {
		log.Warn("verify: invalid or expired token")
		writeError(w, http.StatusBadRequest, "invalid or expired verification link")
		return
	}

	email := user.ID
	if user.Email != nil {
		email = *user.Email
	}
	log.Info("e-mail verified", "user_id", user.ID, "email", email, "perms", user.Perms, "email_changed", emailChanged)

	if !user.Perms.AtLeast(models.PermsGranted) {
		writeJSON(w, http.StatusOK, map[string]any{
			"detail":        "e-mail verified, account awaiting approval",
			"perms":         string(user.Perms),
			"email_changed": emailChanged,
		})
		return
	}
	token, err := auth.IssueToken(user.ID, email, h.JWTSecret, auth.DefaultTokenTTL)
	if err != nil {
		log.Error("verify: could not issue token", "user_id", user.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not issue token")
		return
	}
	user.Login = email
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": user, "email_changed": emailChanged})
}

// logout gère POST /auth/logout : révoque le token courant.
func (h *Handlers) logout(w http.ResponseWriter, r *http.Request) {
	log := logger(r.Context())

	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	if err := h.Store.RevokeToken(r.Context(), claims.ID, claims.ExpiresAt); err != nil {
		log.Error("logout: could not revoke token", "user_id", claims.Sub, "error", err)
		writeError(w, http.StatusInternalServerError, "could not revoke token")
		return
	}
	log.Info("user logged out", "user_id", claims.Sub)
	w.WriteHeader(http.StatusNoContent)
}

// sendVerification crée un jeton de vérification et l'envoie par e-mail.
func (h *Handlers) sendVerification(ctx context.Context, userID, email string) error {
	token, hash, err := auth.NewVerificationToken()
	if err != nil {
		return fmt.Errorf("generate token: %w", err)
	}
	if err := h.Store.CreateEmailVerification(ctx, userID, hash, time.Now().Add(auth.VerificationTokenTTL)); err != nil {
		return fmt.Errorf("store token: %w", err)
	}

	link := h.PublicURL + "/verify-email?token=" + url.QueryEscape(token)
	body := fmt.Sprintf(`Bonjour,

Merci de vous être inscrit sur Douceville. Pour activer votre compte,
confirmez votre adresse e-mail en ouvrant ce lien (valable %d heures) :

%s

Si vous n'êtes pas à l'origine de cette inscription, ignorez ce message.
`, int(auth.VerificationTokenTTL.Hours()), link)

	// L'envoi ne doit pas être interrompu si le client se déconnecte.
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return h.Mailer.Send(sendCtx, email, "Douceville — confirmez votre adresse e-mail", body)
}

// login gère POST /auth/login.
func (h *Handlers) login(w http.ResponseWriter, r *http.Request) {
	log := logger(r.Context())

	var body models.Credentials
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Warn("login: invalid JSON body", "error", err)
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.Email = strings.TrimSpace(strings.ToLower(body.Email))

	rec, err := h.Store.GetUserByEmail(r.Context(), body.Email)
	if err != nil {
		log.Error("login: user lookup failed", "email", body.Email, "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Message uniforme pour ne pas révéler l'existence d'un compte.
	if rec == nil {
		log.Warn("login failed: unknown email", "email", body.Email)
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if auth.VerifyPassword(rec.PasswordHash, body.Password) != nil {
		log.Warn("login failed: wrong password", "email", body.Email, "user_id", rec.ID)
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if rec.Perms == models.PermsUnverified {
		log.Warn("login refused: e-mail not verified", "email", body.Email, "user_id", rec.ID)
		writeJSON(w, http.StatusForbidden, map[string]string{
			"detail": "e-mail address not verified",
			"code":   errCodeEmailNotVerified,
		})
		return
	}
	if !rec.Perms.AtLeast(models.PermsGranted) {
		log.Warn("login refused: account not granted", "email", body.Email, "user_id", rec.ID, "perms", rec.Perms)
		writeJSON(w, http.StatusForbidden, map[string]string{
			"detail": "account awaiting approval",
			"code":   auth.CodeAccountNotGranted,
		})
		return
	}

	email := rec.ID
	if rec.Email != nil {
		email = *rec.Email
	}
	log.Info("user logged in", "user_id", rec.ID, "email", email)
	h.issueAndRespond(w, r, rec.DvUser, email, http.StatusOK)
}

// issueAndRespond émet un JWT pour l'utilisateur et renvoie {token, user}.
func (h *Handlers) issueAndRespond(w http.ResponseWriter, r *http.Request, user models.DvUser, email string, status int) {
	token, err := auth.IssueToken(user.ID, email, h.JWTSecret, auth.DefaultTokenTTL)
	if err != nil {
		logger(r.Context()).Error("could not issue token", "user_id", user.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not issue token")
		return
	}
	user.Login = email
	writeJSON(w, status, models.AuthResponse{Token: token, User: user})
}

// Codes d'erreur transmis au frontend (paramètre ?oidc_error=) à l'issue d'un
// flux OIDC.
const (
	oidcErrFailed     = "failed"
	oidcErrEmailTaken = "email_taken"
)

// oidcConfig gère GET /auth/oidc/config : indique au frontend si la connexion
// OIDC est disponible et le libellé de son bouton.
func (h *Handlers) oidcConfig(w http.ResponseWriter, r *http.Request) {
	if h.OIDC == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "name": h.OIDC.DisplayName()})
}

// oidcLogin gère GET /auth/oidc/login : redirige vers le fournisseur OIDC.
func (h *Handlers) oidcLogin(w http.ResponseWriter, r *http.Request) {
	if h.OIDC == nil {
		writeError(w, http.StatusNotFound, "OIDC login is not configured")
		return
	}
	u, err := h.OIDC.AuthURL(w)
	if err != nil {
		logger(r.Context()).Error("oidc: could not start flow", "error", err)
		writeError(w, http.StatusInternalServerError, "could not start OIDC login")
		return
	}
	http.Redirect(w, r, u, http.StatusFound)
}

// oidcCallback gère GET /auth/oidc/callback : retour du fournisseur. Le compte
// est retrouvé (ou créé), puis le navigateur est renvoyé vers l'application avec
// un token Douceville dans le fragment d'URL (jamais transmis au serveur), ou
// avec un code d'erreur ?oidc_error=.
func (h *Handlers) oidcCallback(w http.ResponseWriter, r *http.Request) {
	log := logger(r.Context())
	if h.OIDC == nil {
		writeError(w, http.StatusNotFound, "OIDC login is not configured")
		return
	}
	fail := func(code string) {
		http.Redirect(w, r, "/?oidc_error="+url.QueryEscape(code), http.StatusFound)
	}

	id, err := h.OIDC.Exchange(w, r)
	if err != nil {
		log.Warn("oidc: callback rejected", "error", err)
		fail(oidcErrFailed)
		return
	}

	user, err := h.Store.FindOrCreateOIDCUser(r.Context(), id, h.OIDC.NewUserPerms())
	if err != nil {
		if errors.Is(err, store.ErrEmailTaken) {
			log.Warn("oidc: e-mail already used by another account (not verified by provider)",
				"issuer", id.Issuer, "subject", id.Subject, "email", id.Email)
			fail(oidcErrEmailTaken)
			return
		}
		log.Error("oidc: could not find or create user", "issuer", id.Issuer, "subject", id.Subject, "error", err)
		fail(oidcErrFailed)
		return
	}

	if !user.Perms.AtLeast(models.PermsGranted) {
		log.Warn("oidc login refused: account not granted", "user_id", user.ID, "perms", user.Perms)
		fail(auth.CodeAccountNotGranted)
		return
	}

	email := user.ID
	if user.Email != nil {
		email = *user.Email
	}
	token, err := auth.IssueToken(user.ID, email, h.JWTSecret, auth.DefaultTokenTTL)
	if err != nil {
		log.Error("oidc: could not issue token", "user_id", user.ID, "error", err)
		fail(oidcErrFailed)
		return
	}
	log.Info("user logged in via OIDC", "user_id", user.ID, "email", email, "issuer", id.Issuer)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/#oidc_token="+url.QueryEscape(token), http.StatusFound)
}
