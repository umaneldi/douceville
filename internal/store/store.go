// Package store encapsule l'accès à la base PostgreSQL/PostGIS via pgx.
package store

import (
	"context"
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
	// VerifyEmail consomme le jeton et fait passer le compte de 'unverified' à
	// 'verified'. Renvoie
	// (nil, nil) si le jeton est inconnu, déjà utilisé ou expiré.
	VerifyEmail(ctx context.Context, tokenHash string) (*models.DvUser, error)
	// RevokeToken révoque un JWT (déconnexion) jusqu'à sa date d'expiration.
	RevokeToken(ctx context.Context, jti string, expiresAt time.Time) error
	// IsTokenRevoked indique si un JWT a été révoqué.
	IsTokenRevoked(ctx context.Context, jti string) (bool, error)
	// GetEtablissementByUAI renvoie un établissement et ses résultats, ou (nil, nil) si absent.
	GetEtablissementByUAI(ctx context.Context, uai string) (*models.Etablissement, error)
	// FindEtablissementsInZone renvoie les établissements contenus dans le polygone WKT,
	// filtrés optionnellement par nature et secteur.
	FindEtablissementsInZone(ctx context.Context, polygonWKT string, nature, secteur []string) ([]models.Etablissement, error)
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
