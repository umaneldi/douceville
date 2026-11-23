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
	// PermsVerified : en attente de validation par un administrateur. La
	// vérification de l'e-mail donne désormais directement 'granted' ; ce niveau
	// sert aux comptes existants, aux comptes OIDC (OIDC_DEFAULT_PERMS) et aux
	// choix des administrateurs.
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

// Valid indique si p est un niveau d'accès connu.
func (p Perms) Valid() bool {
	_, ok := permsRank[p]
	return ok
}

// PermsUpdate est le corps de PATCH /admin/users/{id}.
type PermsUpdate struct {
	Perms Perms `json:"perms"`
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
	// HasPassword indique si le compte a un mot de passe (faux pour un compte
	// créé via OIDC).
	HasPassword bool `json:"has_password"`
	// PendingEmail est la nouvelle adresse en attente de vérification, le cas échéant.
	PendingEmail *string `json:"pending_email"`
	// BalanceCents est le solde de crédit prépayé du compte, en centimes.
	BalanceCents int64 `json:"balance_cents"`
}

// UserRecord est la représentation interne d'un utilisateur, incluant le hash
// du mot de passe. Il ne doit jamais être sérialisé vers le client.
type UserRecord struct {
	DvUser
	PasswordHash string `json:"-"`
}

// OIDCIdentity est l'identité renvoyée par un fournisseur OpenID Connect
// après vérification de l'ID token.
type OIDCIdentity struct {
	Issuer        string
	Subject       string
	Email         string
	EmailVerified bool
	GivenName     string
	FamilyName    string
	Picture       string
}

// ProfileUpdate est le corps de PATCH /user (champ vide ou absent : effacé).
type ProfileUpdate struct {
	FirstName *string `json:"first_name"`
	LastName  *string `json:"last_name"`
	AvatarURL *string `json:"avatar_url"`
}

// EmailChangeRequest est le corps de POST /user/email.
type EmailChangeRequest struct {
	Email           string `json:"email"`
	CurrentPassword string `json:"current_password"`
}

// PasswordChangeRequest est le corps de POST /user/password.
type PasswordChangeRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
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
	// Niveau est le plus petit niveau de temps (en secondes) de la zone de
	// recherche contenant l'établissement (null hors recherche par zone).
	Niveau *float64 `json:"niveau,omitempty"`
}

// Isochrone décrit une zone d'accessibilité calculée par OpenRouteService.
// geometry est l'anneau du polygone du temps maximal (dist), chaque point
// étant [lon, lat] ; levels contient les isochrones emboîtées (par temps
// croissant, la dernière étant celle de dist).
type Isochrone struct {
	LonLat   [2]float64   `json:"lonlat"`
	Dist     float64      `json:"dist"`
	Transp   string       `json:"transp"`
	Geometry [][2]float64 `json:"geometry"`
	Levels   []IsoLevel   `json:"levels"`
}

// IsoLevel est une isochrone emboîtée : temps de trajet (secondes) et anneau.
type IsoLevel struct {
	Dist     float64      `json:"dist"`
	Geometry [][2]float64 `json:"geometry"`
}

// ZoneLevel est un niveau de la zone de recherche : intersection des
// isochrones de tous les points de départ pour un temps de trajet (secondes),
// en GeoJSON.
type ZoneLevel struct {
	Dist     float64         `json:"dist"`
	Geometry json.RawMessage `json:"geometry"`
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
	// Origines sont les points de départ de la recherche : la zone est
	// l'intersection de leurs isochrones. Si vide, Adresse/Dist/Transp
	// forment l'unique point de départ.
	Origines []Origine `json:"origines"`
}

// Origine est un point de départ : une adresse, un temps de trajet (en
// secondes) et un mode de transport.
type Origine struct {
	Adresse string  `json:"adresse"`
	Dist    float64 `json:"dist"`
	Transp  string  `json:"transp"`
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
	Fiscalite     *Fiscalite      `json:"fiscalite"`
	Contour       json.RawMessage `json:"contour"`
}

// Fiscalite est la fiscalité locale d'une commune (table fiscalite, dernier
// exercice disponible). TauxTEOM est nul si la commune ne perçoit pas de TEOM.
// Taux = TauxTFB + TauxTEOM (TEOM comptée 0 si absente), en %.
type Fiscalite struct {
	Annee    int      `json:"annee"`
	TauxTFB  float64  `json:"taux_tfb"`
	TauxTEOM *float64 `json:"taux_teom"`
	Taux     float64  `json:"taux"`
}

// ListeEtablissementsAvecIsochrone est la réponse de POST /etablissements.
type ListeEtablissementsAvecIsochrone struct {
	Etablissements []Etablissement `json:"etablissements"`
	// Isochrone est l'isochrone du premier point de départ (compatibilité).
	Isochrone Isochrone `json:"isochrone"`
	// Isochrones contient l'isochrone de chaque point de départ, dans l'ordre.
	Isochrones []Isochrone `json:"isochrones"`
	// Zone est la géométrie GeoJSON de la zone de recherche (intersection des
	// isochrones de temps maximal), null si elle est vide.
	Zone       json.RawMessage   `json:"zone"`
	Communes   []CommuneSecurite `json:"communes"`
	Immobilier ImmobilierZone    `json:"immobilier"`
	// LogementsSociaux sont les positions (bâtiments) de logements sociaux
	// RPLS contenues dans l'isochrone.
	LogementsSociaux []LogementSocialPosition `json:"logements_sociaux"`
	// ZoneLevels sont les niveaux emboîtés de la zone, par temps croissant ;
	// le dernier est la zone elle-même.
	ZoneLevels []ZoneLevel `json:"zone_levels"`
}

// LogementSocialGroupe regroupe les logements sociaux identiques (mêmes
// caractéristiques) d'une même position ; Nb est leur nombre.
type LogementSocialGroupe struct {
	TypeConstruction  *string `json:"type_construction"`
	TypeDroit         *string `json:"type_droit_immobilier"`
	NbPieces          *int    `json:"nb_pieces"`
	Surface           *int    `json:"surface"`
	AnneeConstruction *int    `json:"annee_construction"`
	Nb                int     `json:"nb"`
}

// LogementSocialPosition est une position (en pratique un bâtiment) portant
// NbLogements logements sociaux, détaillés par groupes identiques.
type LogementSocialPosition struct {
	Lon         float64                `json:"lon"`
	Lat         float64                `json:"lat"`
	NbLogements int                    `json:"nb_logements"`
	Logements   []LogementSocialGroupe `json:"logements"`
	// Niveau est le plus petit niveau de temps (secondes) de la zone contenant
	// la position.
	Niveau *float64 `json:"niveau,omitempty"`
}

// PrixM2 est le prix au m² médian (valeur_fonciere / surface_reelle_bati) des
// ventes DVF de la table immobilier, et le nombre de ces ventes.
type PrixM2 struct {
	PrixM2 int `json:"prix_m2"`
	Ventes int `json:"ventes"`
}

// QuartierImmobilier est un quartier IRIS touché par l'isochrone, avec son
// prix au m² (nul si aucune vente) et son contour GeoJSON (MultiPolygon, WGS84).
type QuartierImmobilier struct {
	Iris        string          `json:"iris"`
	CodeCommune string          `json:"code_commune"`
	Nom         string          `json:"nom"`
	Prix        *PrixM2         `json:"prix"`
	Contour     json.RawMessage `json:"contour"`
}

// CommuneImmobilier est le prix au m² d'une commune : médiane des ventes de
// tous ses IRIS. Le contour est celui de CommuneSecurite (même code_commune).
type CommuneImmobilier struct {
	CodeCommune string  `json:"code_commune"`
	Prix        *PrixM2 `json:"prix"`
}

// ImmobilierZone regroupe les prix au m² des quartiers et des communes touchés
// par l'isochrone, calculés sur toutes les ventes de la table, dont DateMin et
// DateMax (AAAA-MM-JJ, nulles si la table est vide) bornent la période.
type ImmobilierZone struct {
	DateMin   *string              `json:"date_min"`
	DateMax   *string              `json:"date_max"`
	Communes  []CommuneImmobilier  `json:"communes"`
	Quartiers []QuartierImmobilier `json:"quartiers"`
}
