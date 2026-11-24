# Paquet `store`

Accès à la base **PostgreSQL + PostGIS** via pgx (SQL écrit à la main, sans
ORM) et migrations du schéma embarquées dans le binaire. Le schéma est
documenté dans [`schema.md`](../../schema.md).

## Fichiers

| Fichier | Contenu |
| ------- | ------- |
| `store.go` | Interface `Store` (toutes les opérations utilisées par l'API), implémentation `PgStore` (`New(pool)`), erreur `ErrEmailTaken`. |
| `queries.go` | Comptes (création, profil, niveaux, suppression), vérification d'e-mail et changement d'adresse, révocation de tokens, identités OIDC ; requêtes spatiales de la recherche (établissements, communes, prix immobiliers, logements sociaux, intersection des zones, niveau des points). |
| `billing.go` | Tarifs, débit / remboursement / ajustement du crédit, journal des mouvements, paiements. Erreurs `ErrInsufficientCredit`, `ErrNotFound`. |
| `migrate.go` | `Migrate` (applique les migrations embarquées) et `CanMigrate` (le rôle connecté peut-il modifier le schéma ?). |
| `migrations/NNN_*.sql` | Scripts SQL idempotents, appliqués dans l'ordre lexical. |

## Migrations

- Embarquées par `//go:embed migrations/*.sql` et **toutes rejouées à chaque
  démarrage**, sans table de suivi : chaque script doit donc être idempotent
  (`CREATE … IF NOT EXISTS`, `ADD COLUMN IF NOT EXISTS`, `DO $$ … $$` gardés).
- Une évolution du schéma = **un nouveau fichier** numéroté
  (`017_…sql`) ; ne jamais modifier une migration déjà publiée.
- Si le rôle PostgreSQL est en lecture seule (réplica, ou rôle créé par
  `scripts/create_readonly_user.py`), `CanMigrate` renvoie `false` et le
  serveur démarre sans migrer.
- Chaque migration doit être reportée dans `schema.md`.

## Points d'attention

- **SRID** : `etablissement.position` et `logement_social.position` sont en
  SRID 0, alors que `commune.contour` et `quartier.contour` sont en 4326 ; les
  WKT de zone (sans SRID) sont adaptés en conséquence dans chaque requête.
- `IntersectZones` rend les polygones valides (`ST_MakeValid`) et ne garde que
  les parties surfaciques de l'intersection.
- **Crédit** : tout mouvement de solde passe par `applyCredit` dans une
  transaction, qui met à jour `utilisateur.balance_cents` et journalise dans
  `credit_transaction` ; le solde ne peut jamais devenir négatif (contrainte
  `CHECK` + `ErrInsufficientCredit`). `MarkPaymentPaid` est idempotent.
- Un compte créé (inscription ou OIDC) reçoit le crédit de bienvenue configuré
  dans la même transaction.
- Les identifiants de compte sont des UUID v4 générés côté application
  (`newUUID`) ; les e-mails sont comparés sans tenir compte de la casse.
- Toute méthode ajoutée à l'interface `Store` doit aussi l'être aux doublures
  de test de `internal/api` (`mockStore`, `fakeBill`).

## Dépendances

`internal/models`, `github.com/jackc/pgx/v5`.

## Tests

Les requêtes sont testées sur une **vraie base PostGIS** ; sans la variable
`DV_TEST_DSN`, ces tests sont ignorés (`go test ./...` reste donc exécutable
sans base).

```bash
DV_TEST_DSN=postgres://test:test@127.0.0.1:55432/test go test -cover ./internal/store/
```

- `newTestStore(t)` (`testdb_test.go`) crée une base au nom aléatoire sur le
  serveur de `DV_TEST_DSN`, y applique les migrations **deux fois** (elles
  doivent être rejouables) et la supprime en fin de test. Le rôle doit avoir le
  droit `CREATEDB` ; la base de `DV_TEST_DSN` elle-même n'est pas modifiée.
- `mustExec` charge des données de préparation ; `seedZone`
  (`zone_integration_test.go`) charge un petit jeu de données spatial (communes,
  IRIS, ventes DVF, sécurité, fiscalité, établissements, résultats, logements
  sociaux) autour du carré `testZone`.

| Fichier | Couvre |
| ------- | ------ |
| `users_integration_test.go` | Comptes, vérification et changement d'e-mail, révocation de tokens, identités OIDC, `CanMigrate` (rôle en lecture seule), `newUUID`. |
| `zone_integration_test.go` | Établissements (filtres, résultats), communes (score, fiscalité), prix immobiliers, logements sociaux, `IntersectZones`, `PointLevels`. |
| `billing_integration_test.go` | Tarifs, débit / remboursement / ajustement, paiements idempotents, journal. |

Toute nouvelle requête ou migration doit être exercée par un de ces tests.
La CI fournit un service PostGIS et définit `DV_TEST_DSN`.
