package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/umaneldi/douceville/internal/models"
)

// listUsers gère GET /admin/users : tous les comptes.
func (h *Handlers) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.Store.ListUsers(r.Context())
	if err != nil {
		logger(r.Context()).Error("admin: could not list users", "error", err)
		writeError(w, http.StatusInternalServerError, "could not list users")
		return
	}
	writeJSON(w, http.StatusOK, users)
}

// setUserPerms gère PATCH /admin/users/{id} : change le niveau d'accès d'un
// compte. Un administrateur ne peut pas changer son propre niveau (il reste
// ainsi toujours au moins un administrateur). Un compte qui obtient l'accès
// à l'application en est averti par e-mail.
func (h *Handlers) setUserPerms(w http.ResponseWriter, r *http.Request) {
	log := logger(r.Context())
	admin, ok := currentUser(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")

	var body models.PermsUpdate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !body.Perms.Valid() {
		writeError(w, http.StatusUnprocessableEntity, "perms must be one of unverified, verified, granted, admin")
		return
	}
	if id == admin.ID {
		writeError(w, http.StatusUnprocessableEntity, "you cannot change your own level")
		return
	}

	user, previous, err := h.Store.SetUserPerms(r.Context(), id, body.Perms)
	if err != nil {
		log.Error("admin: could not set perms", "admin_id", admin.ID, "user_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not change the level")
		return
	}
	if user == nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	user.Login = ""
	if user.Email != nil {
		user.Login = *user.Email
	}
	log.Info("admin: perms changed", "admin_id", admin.ID, "user_id", id, "old", previous, "new", user.Perms)

	if !previous.AtLeast(models.PermsGranted) && user.Perms.AtLeast(models.PermsGranted) {
		h.sendApproval(r.Context(), user)
	}
	writeJSON(w, http.StatusOK, user)
}

// sendApproval prévient le titulaire du compte que son accès a été validé
// (best effort : un échec est seulement journalisé).
func (h *Handlers) sendApproval(ctx context.Context, user *models.DvUser) {
	if h.Mailer == nil || user.Email == nil || *user.Email == "" {
		return
	}
	body := fmt.Sprintf(`Bonjour,

Votre compte Douceville a été validé par un administrateur.
Vous pouvez dès maintenant vous connecter :

%s/

À bientôt sur Douceville.
`, h.PublicURL)

	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := h.Mailer.Send(sendCtx, *user.Email, "Douceville — votre compte a été validé", body); err != nil {
		logger(ctx).Warn("admin: could not send approval e-mail", "user_id", user.ID, "error", err)
	}
}

// deleteUser gère DELETE /admin/users/{id}. Un administrateur ne peut pas
// supprimer son propre compte.
func (h *Handlers) deleteUser(w http.ResponseWriter, r *http.Request) {
	log := logger(r.Context())
	admin, ok := currentUser(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if id == admin.ID {
		writeError(w, http.StatusUnprocessableEntity, "you cannot delete your own account")
		return
	}

	deleted, err := h.Store.DeleteUser(r.Context(), id)
	if err != nil {
		log.Error("admin: could not delete user", "admin_id", admin.ID, "user_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not delete the user")
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	log.Info("admin: user deleted", "admin_id", admin.ID, "user_id", id)
	w.WriteHeader(http.StatusNoContent)
}
