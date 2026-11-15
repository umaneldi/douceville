// Package models contient les structures de données exposées par l'API.
// Les noms de champs JSON reproduisent exactement ceux du backend Python
// (src/douceville/schemas.py) pour garantir la compatibilité des clients.
package models

import (
	"encoding/json"
	"time"
)

// DvUser représente un utilisateur authentifié (forme publique, sérialisée en JSON).
type DvUser struct {
	ID          string   `json:"id"`
	Login       string   `json:"login"`
	FirstName   *string  `json:"first_name"`
	LastName    *string  `json:"last_name"`
	Email       *string  `json:"email"`
	AvatarURL   *string  `json:"avatar_url"`
	Permissions []string `json:"permissions"`
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

// AuthResponse est renvoyé par /auth/register et /auth/login.
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

// ListeEtablissementsAvecIsochrone est la réponse de POST /etablissements.
type ListeEtablissementsAvecIsochrone struct {
	Etablissements []Etablissement `json:"etablissements"`
	Isochrone      Isochrone       `json:"isochrone"`
}
