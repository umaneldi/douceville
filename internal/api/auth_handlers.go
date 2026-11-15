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
	var body models.Credentials
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	body.Email = strings.TrimSpace(strings.ToLower(body.Email))
	if body.Email == "" || !strings.Contains(body.Email, "@") {
		writeError(w, http.StatusUnprocessableEntity, "a valid email is required")
		return
	}
	if len(body.Password) < minPasswordLen {
		writeError(w, http.StatusUnprocessableEntity, "password must be at least 8 characters")
		return
	}

	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not hash password")
		return
	}

	user, err := h.Store.CreateUser(r.Context(), body.Email, hash, body.FirstName, body.LastName)
	if err != nil {
		if errors.Is(err, store.ErrEmailTaken) {
			writeError(w, http.StatusConflict, "email already registered")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.issueAndRespond(w, *user, body.Email, http.StatusCreated)
}

// login gère POST /auth/login.
func (h *Handlers) login(w http.ResponseWriter, r *http.Request) {
	var body models.Credentials
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.Email = strings.TrimSpace(strings.ToLower(body.Email))

	rec, err := h.Store.GetUserByEmail(r.Context(), body.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Message uniforme pour ne pas révéler l'existence d'un compte.
	if rec == nil || auth.VerifyPassword(rec.PasswordHash, body.Password) != nil {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	email := rec.ID
	if rec.Email != nil {
		email = *rec.Email
	}
	h.issueAndRespond(w, rec.DvUser, email, http.StatusOK)
}

// issueAndRespond émet un JWT pour l'utilisateur et renvoie {token, user}.
func (h *Handlers) issueAndRespond(w http.ResponseWriter, user models.DvUser, email string, status int) {
	token, err := auth.IssueToken(user.ID, email, h.JWTSecret, auth.DefaultTokenTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not issue token")
		return
	}
	user.Login = email
	writeJSON(w, status, models.AuthResponse{Token: token, User: user})
}
