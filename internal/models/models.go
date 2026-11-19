// Package models contient les structures de données exposées par l'API.
// Les noms de champs JSON reproduisent exactement ceux du backend Python
// (src/douceville/schemas.py) pour garantir la compatibilité des clients.
package models

import (
	"encoding/json"
	"time"
)

// Perms est le niveau d'accès d'un compte (enum SQL `user_perms`). Les niveaux
// sont ordonnés : unverified < verified < granted < admin.
type Perms string

// Niveaux d'accès, par ordre croissant.
const (
	// PermsUnverified : compte créé, adresse e-mail non vérifiée.
	PermsUnverified Perms = "unverified"
	// PermsVerified : adresse vérifiée, en attente de validation par un administrateur.
	PermsVerified Perms = "verified"
	// PermsGranted : accès à l'application.
	PermsGranted Perms = "granted"
	// PermsAdmin : administrateur.
	PermsAdmin Perms = "admin"
)

var permsRank = map[Perms]int{
	PermsUnverified: 0,
	PermsVerified:   1,
	PermsGranted:    2,
	PermsAdmin:      3,
}

// AtLeast indique si p est supérieur ou égal à min. Un niveau inconnu n'atteint
// aucun seuil.
func (p Perms) AtLeast(min Perms) bool {
	r, ok := permsRank[p]
	return ok && r >= permsRank[min]
}

// DvUser représente un utilisateur authentifié (forme publique, sérialisée en JSON).
type DvUser struct {
	ID          string   `json:"id"`
	Login       string   `json:"login"`
	FirstName   *string  `json:"first_name"`
	LastName    *string  `json:"last_name"`
	Email       *string  `json:"email"`
	AvatarURL   *string  `json:"avatar_url"`
	Permissions []string `json:"permissions"`
	Perms       Perms    `json:"perms"`
}

// UserRecord est la représentation interne d'un utilisateur, incluant le hash
// du mot de passe. Il ne doit jamais être sérialisé vers le client.
type UserRecord struct {
	DvUser
	PasswordHash string `json:"-"`
}

// Credentials est le corps des requêtes d'inscription et de connexion.
type Credentials struct {
	Email     string  `json:"email"`
	Password  string  `json:"password"`
	FirstName *string `json:"first_name"`
	LastName  *string `json:"last_name"`
}

// EmailRequest est le corps de POST /auth/resend-verification.
type EmailRequest struct {
	Email string `json:"email"`
}

// TokenRequest est le corps de POST /auth/verify.
type TokenRequest struct {
	Token string `json:"token"`
}

// AuthResponse est renvoyé par /auth/login et /auth/verify.
type AuthResponse struct {
	Token string `json:"token"`
	User  DvUser `json:"user"`
}

// Resultat est un résultat d'examen rattaché à un établissement.
type Resultat struct {
	Idx              int     `json:"idx"`
	Diplome          string  `json:"diplome"`
	Annee            int     `json:"annee"`
	Presents         *int    `json:"presents"`
	Admis            *int    `json:"admis"`
	Mentions         *int    `json:"mentions"`
	EtablissementUAI *string `json:"etablissement_uai"`
}

// Etablissement est un établissement scolaire. Le champ Position est le GeoJSON
// renvoyé par PostGIS (ST_AsGeoJSON), transmis tel quel.
type Etablissement struct {
	ID          int             `json:"id"`
	UAI         string          `json:"UAI"`
	Nom         string          `json:"nom"`
	Adresse     *string         `json:"adresse"`
	LieuDit     *string         `json:"lieu_dit"`
	CodePostal  string          `json:"code_postal"`
	Commune     string          `json:"commune"`
	Position    json.RawMessage `json:"position"`
	Departement string          `json:"departement"`
	Academie    *string         `json:"academie"`
	Secteur     string          `json:"secteur"`
	Ouverture   *time.Time      `json:"ouverture"`
	Nature      string          `json:"nature"`
	Resultats   []Resultat      `json:"resultats"`
}

// Isochrone décrit une zone d'accessibilité calculée par OpenRouteService.
// geometry est l'anneau du polygone, chaque point étant [lon, lat].
type Isochrone struct {
	LonLat   [2]float64   `json:"lonlat"`
	Dist     float64      `json:"dist"`
	Transp   string       `json:"transp"`
	Geometry [][2]float64 `json:"geometry"`
}

// QueryParameters est le corps de POST /etablissements.
// year et stat_min sont acceptés mais ignorés (parité avec le Python).
type QueryParameters struct {
	Year    *int     `json:"year"`
	Nature  []string `json:"nature"`
	Secteur []string `json:"secteur"`
	StatMin *int     `json:"stat_min"`
	Adresse string   `json:"adresse"`
	Dist    float64  `json:"dist"`
	Transp  string   `json:"transp"`
}

// CommuneSecurite est une commune touchée par l'isochrone, avec son score de
// sécurité (vue securite_score) et son contour GeoJSON (MultiPolygon, WGS84).
// Annee, Score et NbIndicateurs sont nuls si aucun indicateur n'est diffusé.
type CommuneSecurite struct {
	CodeCommune   string          `json:"code_commune"`
	Nom           string          `json:"nom"`
	Annee         *int            `json:"annee"`
	Score         *int            `json:"score"`
	NbIndicateurs *int            `json:"nb_indicateurs"`
	Contour       json.RawMessage `json:"contour"`
}

// ListeEtablissementsAvecIsochrone est la réponse de POST /etablissements.
type ListeEtablissementsAvecIsochrone struct {
	Etablissements []Etablissement   `json:"etablissements"`
	Isochrone      Isochrone         `json:"isochrone"`
	Communes       []CommuneSecurite `json:"communes"`
}
