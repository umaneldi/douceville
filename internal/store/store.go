// Package store encapsule l'accès à la base PostgreSQL/PostGIS via pgx.
package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ydethe/douceville/internal/models"
)

// Store définit les opérations de persistance utilisées par l'API. L'interface
// permet de tester les handlers sans base de données réelle.
type Store interface {
	// GetUser renvoie le profil utilisateur par identifiant, ou (nil, nil) si absent.
	GetUser(ctx context.Context, id string) (*models.DvUser, error)
	// GetEtablissementByUAI renvoie un établissement et ses résultats, ou (nil, nil) si absent.
	GetEtablissementByUAI(ctx context.Context, uai string) (*models.Etablissement, error)
	// FindEtablissementsInZone renvoie les établissements contenus dans le polygone WKT,
	// filtrés optionnellement par nature et secteur.
	FindEtablissementsInZone(ctx context.Context, polygonWKT string, nature, secteur []string) ([]models.Etablissement, error)
}

// PgStore est l'implémentation pgx de Store.
type PgStore struct {
	pool *pgxpool.Pool
}

// New crée un PgStore à partir d'un pool de connexions.
func New(pool *pgxpool.Pool) *PgStore {
	return &PgStore{pool: pool}
}
