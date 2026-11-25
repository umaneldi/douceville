// Package store encapsule l'accès à la base PostgreSQL/PostGIS via pgx.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/umaneldi/douceville/internal/models"
)

// Store définit les opérations de persistance utilisées par l'API. L'interface
// permet de tester les handlers sans base de données réelle.
type Store interface {
	// GetUser renvoie le profil utilisateur par identifiant, ou (nil, nil) si absent.
	GetUser(ctx context.Context, id string) (*models.DvUser, error)
	// GetUserByEmail renvoie l'utilisateur (hash inclus) par email, ou (nil, nil) si absent.
	GetUserByEmail(ctx context.Context, email string) (*models.UserRecord, error)
	// CreateUser insère un nouvel utilisateur et renvoie son profil public.
	// Renvoie ErrEmailTaken si l'email est déjà utilisé.
	CreateUser(ctx context.Context, email, passwordHash string, firstName, lastName *string) (*models.DvUser, error)
	// CreateEmailVerification enregistre un jeton de vérification (hashé) pour
	// l'utilisateur, en remplaçant les jetons précédents.
	CreateEmailVerification(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error
	// PeekEmailVerification renvoie, sans le consommer, le compte visé par un
	// jeton de vérification valide et l'adresse confirmée (nouvelle adresse pour
	// un changement d'e-mail) ; ok=false si le jeton est inconnu ou expiré.
	PeekEmailVerification(ctx context.Context, tokenHash string) (userID, email string, ok bool, err error)
	// VerifyEmail consomme le jeton. Un jeton d'inscription fait passer le compte
	// à 'granted' (accès direct) ; un jeton de changement d'adresse remplace
	// l'e-mail du compte (emailChanged=true, ErrEmailTaken si l'adresse a été prise
	// entre-temps). Renvoie (nil, false, nil) si le jeton est inconnu, déjà utilisé
	// ou expiré.
	VerifyEmail(ctx context.Context, tokenHash string) (user *models.DvUser, emailChanged bool, err error)
	// GetUserRecordByID renvoie l'utilisateur (hash du mot de passe inclus, vide
	// si le compte n'en a pas) par identifiant, ou (nil, nil) si absent.
	GetUserRecordByID(ctx context.Context, id string) (*models.UserRecord, error)
	// UpdateProfile met à jour nom, prénom et avatar, et renvoie le profil.
	UpdateProfile(ctx context.Context, id string, p models.ProfileUpdate) (*models.DvUser, error)
	// SetPassword remplace le hash du mot de passe du compte.
	SetPassword(ctx context.Context, id, passwordHash string) error
	// CreateEmailChange enregistre un jeton (hashé) confirmant le passage du
	// compte à newEmail, en remplaçant les demandes de changement précédentes.
	CreateEmailChange(ctx context.Context, userID, newEmail, tokenHash string, expiresAt time.Time) error
	// RevokeToken révoque un JWT (déconnexion) jusqu'à sa date d'expiration.
	RevokeToken(ctx context.Context, jti string, expiresAt time.Time) error
	// IsTokenRevoked indique si un JWT a été révoqué.
	IsTokenRevoked(ctx context.Context, jti string) (bool, error)
	// FindOrCreateOIDCUser renvoie le compte rattaché à l'identité OIDC, en le
	// créant (niveau defaultPerms) ou en rattachant l'identité au compte de même
	// e-mail si le fournisseur atteste l'adresse (email_verified). Renvoie
	// ErrEmailTaken si l'e-mail appartient à un autre compte et n'est pas attesté.
	FindOrCreateOIDCUser(ctx context.Context, id models.OIDCIdentity, defaultPerms models.Perms) (*models.DvUser, error)
	// ListUsers renvoie tous les comptes, triés par e-mail.
	ListUsers(ctx context.Context) ([]models.DvUser, error)
	// SetUserPerms change le niveau d'accès du compte et renvoie son profil et
	// son niveau précédent, ou (nil, "", nil) si le compte n'existe pas.
	SetUserPerms(ctx context.Context, id string, perms models.Perms) (user *models.DvUser, previous models.Perms, err error)
	// DeleteUser supprime le compte (jetons de vérification et identités OIDC
	// compris) ; renvoie false si le compte n'existe pas.
	DeleteUser(ctx context.Context, id string) (bool, error)
	// GetEtablissementByUAI renvoie un établissement et ses résultats, ou (nil, nil) si absent.
	GetEtablissementByUAI(ctx context.Context, uai string) (*models.Etablissement, error)
	// FindEtablissementsInZone renvoie les établissements contenus dans le
	// polygone WKT qui satisfont le critère de leur nature (nature, secteurs,
	// taux de réussite ; une école primaire satisfait les critères maternelle
	// et élémentaire), avec leur taux et leurs résultats ; sans critère, tous
	// les établissements du polygone.
	FindEtablissementsInZone(ctx context.Context, polygonWKT string, criteres []models.CritereNature) ([]models.Etablissement, error)
	// SelectSourceEtablissements renvoie, pour chaque critère, au plus
	// parNature établissements de sa nature situés à moins de rayonM mètres de
	// centre ([lon, lat]) et satisfaisant le critère, classés par taux de
	// réussite décroissant (inconnu en dernier) puis par distance au centre.
	// Chaque source porte la nature (Critere), le temps et le mode de
	// transport de son critère ; une école primaire peut être retenue pour
	// les critères maternelle et élémentaire.
	SelectSourceEtablissements(ctx context.Context, centre [2]float64, rayonM float64, criteres []models.CritereNature, parNature int) ([]models.Source, error)
	// FilterZone restreint le polygone WKT (coordonnées WGS84) aux quartiers
	// IRIS satisfaisant les filtres f ; mêmes résultats que IntersectUnions.
	FilterZone(ctx context.Context, polygonWKT string, f models.FiltresZone) (wkt string, geojson json.RawMessage, empty bool, err error)
	// GetCachedIsochrones renvoie les anneaux en cache des clés données, créés
	// il y a moins de maxAge, et les marque utilisés.
	GetCachedIsochrones(ctx context.Context, keys []models.IsochroneKey, maxAge time.Duration) (map[models.IsochroneKey][][2]float64, error)
	// PutCachedIsochrones enregistre (ou remplace) des isochrones en cache, puis
	// purge les entrées plus anciennes que maxAge et, au-delà de maxEntries
	// (si > 0), les moins récemment utilisées.
	PutCachedIsochrones(ctx context.Context, entries map[models.IsochroneKey][][2]float64, maxAge time.Duration, maxEntries int) error
	// QuartierProximite renvoie la fiche du quartier IRIS (prix, sécurité,
	// fiscalité) et, pour chaque critère, l'établissement le plus proche qui le
	// satisfait ; (nil, nil) si l'IRIS est inconnu.
	QuartierProximite(ctx context.Context, iris string, criteres []models.CritereNature) (*models.QuartierProximite, error)
	// FindCommunesInZone renvoie les communes dont le contour intersecte le
	// polygone WKT (coordonnées WGS84), avec leur score de sécurité de la
	// dernière année disponible et leur fiscalité locale du dernier exercice.
	FindCommunesInZone(ctx context.Context, polygonWKT string) ([]models.CommuneSecurite, error)
	// FindImmobilierInZone renvoie les prix au m² médians (ventes DVF) des
	// quartiers IRIS intersectant le polygone WKT et, sur tous leurs IRIS,
	// des communes codesCommunes.
	FindImmobilierInZone(ctx context.Context, polygonWKT string, codesCommunes []string) (models.ImmobilierZone, error)
	// FindLogementsSociauxInZone renvoie les logements sociaux géolocalisés
	// contenus dans le polygone WKT, regroupés par position.
	FindLogementsSociauxInZone(ctx context.Context, polygonWKT string) ([]models.LogementSocialPosition, error)
	// IntersectUnions calcule l'union des polygones WKT de chaque groupe, puis
	// l'intersection (surfacique) de ces unions. Elle renvoie son WKT (sans
	// SRID, utilisable par les méthodes Find*InZone), sa géométrie GeoJSON, et
	// empty=true si elle est vide. Il faut au moins un groupe, et aucun vide.
	IntersectUnions(ctx context.Context, groups [][]string) (wkt string, geojson json.RawMessage, empty bool, err error)

	// GetBillingSettings renvoie les tarifs et paramètres de facturation.
	GetBillingSettings(ctx context.Context) (models.BillingSettings, error)
	// UpdateBillingSettings enregistre les tarifs (hors devise) et renvoie les
	// paramètres à jour.
	UpdateBillingSettings(ctx context.Context, b models.BillingSettings, adminID string) (models.BillingSettings, error)
	// DebitCredit débite atomiquement cents du solde du compte et renvoie le
	// nouveau solde ; ErrInsufficientCredit si le solde est insuffisant.
	DebitCredit(ctx context.Context, userID string, cents int64, desc string) (int64, error)
	// RefundCredit recrédite cents (requête payante échouée) et renvoie le solde.
	RefundCredit(ctx context.Context, userID string, cents int64, desc string) (int64, error)
	// AdjustCredit ajoute delta (signé) au solde par un administrateur ;
	// ErrInsufficientCredit si le solde deviendrait négatif, ErrNotFound si le
	// compte n'existe pas.
	AdjustCredit(ctx context.Context, userID string, delta int64, adminID, reason string) (int64, error)
	// ListCreditTransactions renvoie les derniers mouvements du compte.
	ListCreditTransactions(ctx context.Context, userID string, limit int) ([]models.CreditTransaction, error)
	// CreatePayment enregistre un paiement de rechargement.
	CreatePayment(ctx context.Context, p models.Payment) error
	// GetPayment renvoie un paiement, ou (nil, nil) s'il est inconnu.
	GetPayment(ctx context.Context, id string) (*models.Payment, error)
	// MarkPaymentPaid passe le paiement à 'paid' et crédite le compte ;
	// credited=false s'il était inconnu ou déjà crédité (idempotent).
	MarkPaymentPaid(ctx context.Context, id string) (credited bool, err error)
	// UpdatePaymentStatus met à jour le statut d'un paiement non crédité.
	UpdatePaymentStatus(ctx context.Context, id, status string) error
	// ListPendingPayments renvoie les paiements récents non finalisés du compte.
	ListPendingPayments(ctx context.Context, userID string) ([]models.Payment, error)
}

// ErrEmailTaken est renvoyée par CreateUser quand l'email existe déjà.
var ErrEmailTaken = errors.New("email already registered")

// PgStore est l'implémentation pgx de Store.
type PgStore struct {
	pool *pgxpool.Pool
}

// New crée un PgStore à partir d'un pool de connexions.
func New(pool *pgxpool.Pool) *PgStore {
	return &PgStore{pool: pool}
}
