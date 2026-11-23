package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/umaneldi/douceville/internal/auth"
	"github.com/umaneldi/douceville/internal/models"
)

const (
	maxNameLen      = 100
	maxAvatarURLLen = 2048
	// errCodeInvalidPassword : mot de passe actuel incorrect (403 et non 401, qui
	// déconnecterait le client).
	errCodeInvalidPassword = "invalid_password"
)

// currentUser renvoie l'utilisateur authentifié, ou écrit une erreur 401.
func currentUser(w http.ResponseWriter, r *http.Request) (*models.DvUser, bool) {
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Not authenticated")
	}
	return u, ok
}

// cleanOptional supprime les espaces ; une valeur vide devient nil.
func cleanOptional(s *string) *string {
	if s == nil {
		return nil
	}
	v := strings.TrimSpace(*s)
	if v == "" {
		return nil
	}
	return &v
}

// updateProfile gère PATCH /user : prénom, nom et URL de l'avatar.
func (h *Handlers) updateProfile(w http.ResponseWriter, r *http.Request) {
	log := logger(r.Context())
	user, ok := currentUser(w, r)
	if !ok {
		return
	}

	var body models.ProfileUpdate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.FirstName = cleanOptional(body.FirstName)
	body.LastName = cleanOptional(body.LastName)
	body.AvatarURL = cleanOptional(body.AvatarURL)

	for _, n := range []*string{body.FirstName, body.LastName} {
		if n != nil && utf8.RuneCountInString(*n) > maxNameLen {
			writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("names must be at most %d characters", maxNameLen))
			return
		}
	}
	if body.AvatarURL != nil {
		u, err := url.Parse(*body.AvatarURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(*body.AvatarURL) > maxAvatarURLLen {
			writeError(w, http.StatusUnprocessableEntity, "avatar_url must be a valid http(s) URL")
			return
		}
	}

	updated, err := h.Store.UpdateProfile(r.Context(), user.ID, body)
	if err != nil || updated == nil {
		log.Error("profile: could not update", "user_id", user.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not update profile")
		return
	}
	updated.Login = user.Login
	log.Info("profile updated", "user_id", user.ID)
	writeJSON(w, http.StatusOK, updated)
}

// checkCurrentPassword vérifie le mot de passe actuel si le compte en a un
// (un compte créé via OIDC n'en a pas). Écrit l'erreur et renvoie false en cas d'échec.
func (h *Handlers) checkCurrentPassword(w http.ResponseWriter, r *http.Request, userID, password string) (*models.UserRecord, bool) {
	rec, err := h.Store.GetUserRecordByID(r.Context(), userID)
	if err != nil || rec == nil {
		logger(r.Context()).Error("could not load user", "user_id", userID, "error", err)
		writeError(w, http.StatusInternalServerError, "user lookup failed")
		return nil, false
	}
	if rec.PasswordHash != "" && auth.VerifyPassword(rec.PasswordHash, password) != nil {
		logger(r.Context()).Warn("wrong current password", "user_id", userID)
		writeJSON(w, http.StatusForbidden, map[string]string{
			"detail": "current password is incorrect",
			"code":   errCodeInvalidPassword,
		})
		return nil, false
	}
	return rec, true
}

// changeEmail gère POST /user/email. La nouvelle adresse doit être confirmée par
// le lien envoyé ; l'adresse actuelle reste active jusque-là.
func (h *Handlers) changeEmail(w http.ResponseWriter, r *http.Request) {
	log := logger(r.Context())
	user, ok := currentUser(w, r)
	if !ok {
		return
	}

	var body models.EmailChangeRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	newEmail := strings.TrimSpace(strings.ToLower(body.Email))
	if !validEmail(newEmail) {
		writeError(w, http.StatusUnprocessableEntity, "a valid email is required")
		return
	}
	if user.Email != nil && strings.EqualFold(*user.Email, newEmail) {
		writeError(w, http.StatusUnprocessableEntity, "this is already your e-mail address")
		return
	}

	rec, ok := h.checkCurrentPassword(w, r, user.ID, body.CurrentPassword)
	if !ok {
		return
	}

	existing, err := h.Store.GetUserByEmail(r.Context(), newEmail)
	if err != nil {
		log.Error("email change: lookup failed", "user_id", user.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "user lookup failed")
		return
	}
	if existing != nil {
		log.Warn("email change: address already in use", "user_id", user.ID, "email", newEmail)
		writeError(w, http.StatusConflict, "this e-mail address is already in use")
		return
	}

	if err := h.sendEmailChange(r.Context(), rec, newEmail); err != nil {
		log.Error("email change: could not send confirmation", "user_id", user.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not send the confirmation e-mail")
		return
	}
	log.Info("email change requested", "user_id", user.ID, "new_email", newEmail)
	writeJSON(w, http.StatusAccepted, map[string]string{
		"detail":        "check your new address to confirm the change",
		"pending_email": newEmail,
	})
}

// sendEmailChange crée un jeton de changement d'adresse, envoie le lien de
// confirmation à la nouvelle adresse et prévient l'ancienne.
func (h *Handlers) sendEmailChange(ctx context.Context, rec *models.UserRecord, newEmail string) error {
	token, hash, err := auth.NewVerificationToken()
	if err != nil {
		return fmt.Errorf("generate token: %w", err)
	}
	if err := h.Store.CreateEmailChange(ctx, rec.ID, newEmail, hash, time.Now().Add(auth.VerificationTokenTTL)); err != nil {
		return fmt.Errorf("store token: %w", err)
	}

	link := h.PublicURL + "/verify-email?token=" + url.QueryEscape(token)
	body := fmt.Sprintf(`Bonjour,

Une demande de changement d'adresse e-mail a été faite sur votre compte Douceville.
Pour utiliser désormais cette adresse, ouvrez ce lien (valable %d heures) :

%s

Si vous n'êtes pas à l'origine de cette demande, ignorez ce message.
`, int(auth.VerificationTokenTTL.Hours()), link)

	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := h.Mailer.Send(sendCtx, newEmail, "Douceville — confirmez votre nouvelle adresse e-mail", body); err != nil {
		return err
	}

	// Avertissement à l'ancienne adresse (best effort).
	if rec.Email != nil && *rec.Email != "" {
		notice := fmt.Sprintf(`Bonjour,

Une demande de changement de l'adresse e-mail de votre compte Douceville vers
%s vient d'être faite. Elle ne prendra effet qu'une fois confirmée depuis
cette nouvelle adresse.

Si vous n'êtes pas à l'origine de cette demande, changez votre mot de passe.
`, newEmail)
		if err := h.Mailer.Send(sendCtx, *rec.Email, "Douceville — changement d'adresse e-mail demandé", notice); err != nil {
			logger(ctx).Warn("email change: could not notify previous address", "user_id", rec.ID, "error", err)
		}
	}
	return nil
}

// changePassword gère POST /user/password. Un compte sans mot de passe (créé
// via OIDC) peut en définir un sans fournir de mot de passe actuel.
func (h *Handlers) changePassword(w http.ResponseWriter, r *http.Request) {
	log := logger(r.Context())
	user, ok := currentUser(w, r)
	if !ok {
		return
	}

	var body models.PasswordChangeRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(body.NewPassword) < minPasswordLen {
		writeError(w, http.StatusUnprocessableEntity, "password must be at least 8 characters")
		return
	}
	if _, ok := h.checkCurrentPassword(w, r, user.ID, body.CurrentPassword); !ok {
		return
	}

	hash, err := auth.HashPassword(body.NewPassword)
	if err != nil {
		log.Error("password change: could not hash", "user_id", user.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	if err := h.Store.SetPassword(r.Context(), user.ID, hash); err != nil {
		log.Error("password change: could not store", "user_id", user.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not change password")
		return
	}
	log.Info("password changed", "user_id", user.ID)
	w.WriteHeader(http.StatusNoContent)
}
