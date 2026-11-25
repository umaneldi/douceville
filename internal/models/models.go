// Package models contient les structures de données exposées par l'API.
// Les noms de champs JSON reproduisent exactement ceux du backend Python
// (src/douceville/schemas.py) pour garantir la compatibilité des clients.
package models

import (
	"encoding/json"
	"math"
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
	// Taux est le taux de réussite aux examens en % (Σadmis / Σprésents, vue
	// etablissement_taux) ; null sans résultats ou hors recherche.
	Taux *float64 `json:"taux,omitempty"`
}

// Isochrone décrit une zone d'accessibilité calculée par OpenRouteService.
// geometry est l'anneau du polygone du temps maximal (dist), chaque point
// étant [lon, lat] ; levels contient les isochrones emboîtées (par temps
// croissant, la dernière étant celle de dist), absentes pour une isochrone de
// recherche (un seul temps).
type Isochrone struct {
	LonLat   [2]float64   `json:"lonlat"`
	Dist     float64      `json:"dist"`
	Transp   string       `json:"transp"`
	Geometry [][2]float64 `json:"geometry"`
	Levels   []IsoLevel   `json:"levels,omitempty"`
}

// IsoLevel est une isochrone emboîtée : temps de trajet (secondes) et anneau.
type IsoLevel struct {
	Dist     float64      `json:"dist"`
	Geometry [][2]float64 `json:"geometry"`
}

// Natures sont les natures d'établissement acceptées par les critères de
// recherche (valeurs de etablissement.nature).
var Natures = []string{"Maternelle", "Elementaire", "Collège", "Lycée"}

// NaturePrimaire est la nature des écoles primaires (maternelle et
// élémentaire réunies) : elle n'est pas un critère de recherche, mais un
// établissement de cette nature satisfait les critères des natures de
// NaturesPrimaire.
const NaturePrimaire = "Primaire"

// NaturesPrimaire sont les natures de critère que satisfait une école
// primaire.
var NaturesPrimaire = []string{"Maternelle", "Elementaire"}

// Secteurs sont les secteurs d'établissement acceptés par les critères.
var Secteurs = []string{"public", "privé", "hors contrat"}

// Transports sont les profils OpenRouteService acceptés.
var Transports = []string{"driving-car", "cycling-regular", "foot-walking"}

// CritereNature décrit, pour une nature d'établissement, les établissements
// retenus et leur isochrone : secteurs (vide : tous), plage de taux de
// réussite en % (les établissements sans résultats ne sont pas filtrés),
// temps de trajet Dist (secondes) et mode de transport Transp.
type CritereNature struct {
	Nature  string   `json:"nature"`
	Secteur []string `json:"secteur"`
	TauxMin float64  `json:"taux_min"`
	TauxMax float64  `json:"taux_max"`
	Dist    float64  `json:"dist"`
	Transp  string   `json:"transp"`
}

// FiltresZone restreignent la zone de recherche aux quartiers IRIS dont le
// prix au m² (médiane de l'IRIS, à défaut de la commune) est dans
// [PrixM2Min, PrixM2Max] et dont la commune a un score de sécurité >=
// SecuriteMin et un taux de fiscalité (TFB + TEOM) <= FiscaliteMax. Un champ
// nul ne filtre pas ; une valeur inconnue exclut le quartier quand le filtre
// correspondant est actif.
type FiltresZone struct {
	PrixM2Min    *float64 `json:"prix_m2_min"`
	PrixM2Max    *float64 `json:"prix_m2_max"`
	SecuriteMin  *float64 `json:"securite_min"`
	FiscaliteMax *float64 `json:"fiscalite_max"`
}

// Actif indique si au moins un filtre est renseigné.
func (f FiltresZone) Actif() bool {
	return f.PrixM2Min != nil || f.PrixM2Max != nil || f.SecuriteMin != nil || f.FiscaliteMax != nil
}

// SearchRequest est le corps de POST /etablissements : les établissements
// sources sont cherchés dans un rayon RayonKm autour de l'adresse Centre,
// selon un critère par nature ; la zone est l'intersection, entre critères,
// de l'union des isochrones des sources de chaque critère, restreinte par les
// filtres.
type SearchRequest struct {
	Centre   string          `json:"centre"`
	RayonKm  float64         `json:"rayon_km"`
	Criteres []CritereNature `json:"criteres"`
	FiltresZone
}

// ProximiteRequest est le corps de POST /quartiers/{iris}/proximite.
type ProximiteRequest struct {
	Criteres []CritereNature `json:"criteres"`
}

// Centre est le centre géocodé de la recherche et son rayon.
type Centre struct {
	Lon     float64 `json:"lon"`
	Lat     float64 `json:"lat"`
	RayonKm float64 `json:"rayon_km"`
}

// Source est un établissement source de la recherche, avec les paramètres de
// son isochrone (ceux du critère de sa nature) et son anneau [lon, lat]
// (vide tant qu'elle n'est pas calculée).
type Source struct {
	Etablissement
	// Critere est la nature du critère pour lequel la source a été retenue ;
	// elle diffère de Nature pour une école primaire (NaturePrimaire).
	Critere   string       `json:"critere"`
	Dist      float64      `json:"dist"`
	Transp    string       `json:"transp"`
	Isochrone [][2]float64 `json:"isochrone"`
}

// LonLat renvoie la position [lon, lat] de la source ; ok=false si elle est
// absente ou n'est pas un Point GeoJSON.
func (s Source) LonLat() (lonlat [2]float64, ok bool) {
	var p struct {
		Type        string     `json:"type"`
		Coordinates [2]float64 `json:"coordinates"`
	}
	if len(s.Position) == 0 || json.Unmarshal(s.Position, &p) != nil || p.Type != "Point" {
		return [2]float64{}, false
	}
	return p.Coordinates, true
}

// IsochroneKey identifie une isochrone en cache : position arrondie au
// 1e-5 degré, temps (secondes) et mode de transport.
type IsochroneKey struct {
	LonE5  int
	LatE5  int
	Dist   int
	Transp string
}

// NewIsochroneKey construit la clé de cache d'une isochrone.
func NewIsochroneKey(lonlat [2]float64, dist float64, transp string) IsochroneKey {
	return IsochroneKey{
		LonE5:  int(math.Round(lonlat[0] * 1e5)),
		LatE5:  int(math.Round(lonlat[1] * 1e5)),
		Dist:   int(math.Round(dist)),
		Transp: transp,
	}
}

// Proche est l'établissement le plus proche d'un quartier pour une nature
// (critère de recherche) ; DistanceM est la distance à vol d'oiseau en
// mètres, et les autres champs sont nuls si aucun établissement ne correspond.
type Proche struct {
	Nature    string   `json:"nature"`
	DistanceM *float64 `json:"distance_m"`
	UAI       *string  `json:"uai"`
	Nom       *string  `json:"nom"`
	Commune   *string  `json:"commune"`
	Secteur   *string  `json:"secteur"`
	Taux      *float64 `json:"taux"`
}

// SecuriteCommune est le score de sécurité d'une commune (dernière année de
// securite_score).
type SecuriteCommune struct {
	Annee         int `json:"annee"`
	Score         int `json:"score"`
	NbIndicateurs int `json:"nb_indicateurs"`
}

// QuartierProximite est la fiche d'un quartier IRIS : prix au m² (IRIS et
// commune), sécurité et fiscalité de la commune, et établissement le plus
// proche par critère (distance depuis un point intérieur du quartier).
type QuartierProximite struct {
	Iris        string           `json:"iris"`
	Nom         string           `json:"nom"`
	CodeCommune string           `json:"code_commune"`
	Commune     string           `json:"commune"`
	Prix        *PrixM2          `json:"prix"`
	PrixCommune *PrixM2          `json:"prix_commune"`
	Securite    *SecuriteCommune `json:"securite"`
	Fiscalite   *Fiscalite       `json:"fiscalite"`
	Proches     []Proche         `json:"proches"`
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
	// Centre est le centre géocodé de la recherche et son rayon.
	Centre Centre `json:"centre"`
	// Sources sont les établissements retenus par les critères, avec leur
	// isochrone ; la zone est l'intersection, entre natures, de l'union des
	// isochrones de chaque nature.
	Sources []Source `json:"sources"`
	// CriteresVides sont les natures dont le critère ne retient aucun
	// établissement dans le rayon (la zone est alors vide).
	CriteresVides []string `json:"criteres_vides"`
	// ZoneBrute est l'intersection des unions d'isochrones par nature, avant
	// les filtres de zone (GeoJSON), null si elle est vide.
	ZoneBrute json.RawMessage `json:"zone_brute"`
	// Zone est la zone de recherche (ZoneBrute restreinte par les filtres),
	// null si elle est vide.
	Zone           json.RawMessage   `json:"zone"`
	Etablissements []Etablissement   `json:"etablissements"`
	Communes       []CommuneSecurite `json:"communes"`
	Immobilier     ImmobilierZone    `json:"immobilier"`
	// LogementsSociaux sont les positions (bâtiments) de logements sociaux
	// RPLS contenues dans la zone.
	LogementsSociaux []LogementSocialPosition `json:"logements_sociaux"`
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
