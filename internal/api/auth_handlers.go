package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/ydethe/douceville/internal/auth"
	"github.com/ydethe/douceville/internal/models"
	"github.com/ydethe/douceville/internal/store"
)

// minPasswordLen est la longueur minimale acceptée pour un mot de passe.
const minPasswordLen = 8

// register gère POST /auth/register (inscription ouverte).
func (h *Handlers) register(w http.ResponseWriter, r *http.Request) {
	log := logger(r.Context())

	var body models.Credentials
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Warn("register: invalid JSON body", "error", err)
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	body.Email = strings.TrimSpace(strings.ToLower(body.Email))
	if body.Email == "" || !strings.Contains(body.Email, "@") {
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
	h.issueAndRespond(w, r, *user, body.Email, http.StatusCreated)
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
