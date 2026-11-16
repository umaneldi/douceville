package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
)

// CanMigrate indique si le rôle connecté peut appliquer les migrations. Il
// renvoie false pour un rôle en lecture seule : session ou serveur en
// read-only (ex. réplica), ou absence du droit CREATE sur le schéma courant
// (cas d'un rôle créé par scripts/create_readonly_user.py).
func (s *PgStore) CanMigrate(ctx context.Context) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT current_setting('transaction_read_only') = 'off'
		   AND current_schema() IS NOT NULL
		   AND has_schema_privilege(current_schema(), 'CREATE')`).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check migration privileges: %w", err)
	}
	return ok, nil
}

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applique, dans l'ordre lexical, tous les fichiers migrations/*.sql
// embarqués dans le binaire. Les scripts sont idempotents (CREATE ... IF NOT
// EXISTS, ADD COLUMN IF NOT EXISTS), donc l'opération peut être rejouée à
// chaque démarrage sans table de suivi dédiée.
func (s *PgStore) Migrate(ctx context.Context) error {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		sqlBytes, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if _, err := s.pool.Exec(ctx, string(sqlBytes)); err != nil {
			slog.Error("migration failed", "migration", name, "err", err)
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		slog.Info("migration applied", "migration", name)
	}
	return nil
}
