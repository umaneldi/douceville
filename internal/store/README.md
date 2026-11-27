# Paquet `store`

Accès à la base **PostgreSQL + PostGIS** via pgx (SQL écrit à la main, sans
ORM) et migrations du schéma embarquées dans le binaire. Le schéma est
documenté dans [`schema.md`](../../schema.md).

## Fichiers

| Fichier | Contenu |
| ------- | ------- |
| `store.go` | Interface `Store` (toutes les opérations utilisées par l'API), implémentation `PgStore` (`New(pool)`), erreur `ErrEmailTaken`. |
| `queries.go` | Comptes (création, profil, niveaux, suppression), vérification d'e-mail et changement d'adresse, révocation de tokens, identités OIDC ; colonnes d'un établissement (`etabCols`, `etabDest`, partagées avec `search.go`) ; requêtes spatiales de la zone (communes, prix immobiliers, logements sociaux, union/intersection des isochrones). |
| `search.go` | Recherche par établissements : sources par critère (`SelectSourceEtablissements`), établissements de la zone selon les critères (`FindEtablissementsInZone`), filtres de zone (`FilterZone`), cache d'isochrones (`GetCachedIsochrones`, `PutCachedIsochrones`), fiche d'un quartier (`QuartierProximite` : point intérieur du quartier et, par critère, établissement le plus proche avec sa position). |
| `billing.go` | Tarifs, débit / remboursement / ajustement du crédit, journal des mouvements, paiements. Erreurs `ErrInsufficientCredit`, `ErrNotFound`. |
| `migrate.go` | `Migrate` (applique les migrations embarquées) et `CanMigrate` (le rôle connecté peut-il modifier le schéma ?). |
| `migrations/NNN_*.sql` | Scripts SQL idempotents, appliqués dans l'ordre lexical. |

## Migrations

- Embarquées par `//go:embed migrations/*.sql` et **toutes rejouées à chaque
  démarrage**, sans table de suivi : chaque script doit donc être idempotent
  (`CREATE … IF NOT EXISTS`, `ADD COLUMN IF NOT EXISTS`, `DO $$ … $$` gardés).
- Une évolution du schéma = **un nouveau fichier** numéroté
  (`019_…sql`) ; ne jamais modifier une migration déjà publiée.
- Si le rôle PostgreSQL est en lecture seule (réplica, ou rôle créé par
  `scripts/create_readonly_user.py`), `CanMigrate` renvoie `false` et le
  serveur démarre sans migrer.
- Chaque migration doit être reportée dans `schema.md`.

## Points d'attention

- **Sécurité** : le score lu pour l'interface et le filtre de zone est le
  score départemental de la vue `securite_departement` (migration 022), joint
  par `secuDepJoin` (avec le nom du département, table `departement`) ; ses
  colonnes `secuDepColumns` sont lues par `secuDep` (`FindCommunesInZone`,
  `QuartierProximite`). `seedZone` rafraîchit `securite_score` puis
  `securite_departement`.
- **Colonnes d'un établissement** : listées une seule fois par `etabCols`
  (préfixe d'alias, expression de la position) et `etabDest` (destinations de
  `Scan`, même ordre) ; `etabColumns`, `etabColumnsE` et `etabColumnsSel` en
  dérivent. Une nouvelle colonne s'ajoute aux deux fonctions.
- **SRID** : `etablissement.position` et `logement_social.position` sont en
  SRID 0, alors que `commune.contour` et `quartier.contour` sont en 4326 ; les
  WKT de zone (sans SRID) sont adaptés en conséquence dans chaque requête.
- `IntersectUnions` rend les polygones valides (`ST_MakeValid`), les unit au
  sein de chaque groupe (`ST_Union`), intersecte les unions et ne garde que
  les parties surfaciques du résultat ; `FilterZone` renvoie un résultat
  de même forme.
- **Critères** : ils sont passés en tableaux parallèles (`criteresArgs`) et
  dépliés par `unnest` dans la CTE `c` (`criteresCTE`) ; les secteurs d'un
  critère sont joints par `|`, de même que ses filières (vide : pas de
  filtre ; sinon `etablissement.filieres` doit en avoir au moins une en
  commun, opérateur `&&`). La condition commune est `critereMatch` : une
  école primaire (`models.NaturePrimaire`, voir migration 019) satisfait les
  critères des natures `models.NaturesPrimaire` (maternelle, élémentaire) ;
  `SelectSourceEtablissements` renseigne donc `Source.Critere` (nature du
  critère qui a retenu la source), qui peut différer de `Nature`. Un
  établissement sans taux connu ne satisfait la plage de taux d'un critère
  que si elle est complète (0–100). La même condition sert aux sources, aux
  établissements de la zone et aux plus proches d'un quartier
  (`QuartierProximite`).
- **Taux de réussite** : vue `etablissement_taux` (migration 017), même
  définition que `successRate()` dans `web/app.js`. Dans
  `SelectSourceEtablissements`, les candidats du disque puis leur taux sont
  **matérialisés** (CTE `MATERIALIZED`) : sinon le planificateur parcourt
  l'agrégat de la vue pour chaque candidat (plusieurs secondes sur la base
  complète au lieu d'environ 100 ms).
- **Distances** : rayon et distances sont géodésiques
  (`ST_SetSRID(position, 4326)::geography`) ; une boîte englobante en degrés
  (`ST_MakeEnvelope`) ou un tri KNN planaire (`<->`, 5 voisins départagés en
  géodésique) sert l'index GiST en SRID 0.
- **Crédit** : tout mouvement de solde passe par `applyCredit` dans une
  transaction, qui met à jour `utilisateur.balance_cents` et journalise dans
  `credit_transaction` ; le solde ne peut jamais devenir négatif (contrainte
  `CHECK` + `ErrInsufficientCredit`). `MarkPaymentPaid` est idempotent.
- Un compte créé (inscription ou OIDC) reçoit le crédit de bienvenue configuré
  dans la même transaction.
- Les identifiants de compte sont des UUID v4 générés côté application
  (`newUUID`) ; les e-mails sont comparés sans tenir compte de la casse.
- Toute méthode ajoutée à l'interface `Store` doit aussi l'être aux doublures
  de test de `internal/api` (`mockStore`, `fakeBill`, `failingStore`).

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
  sociaux) autour du carré `testZone` ; `lycees` (`search_integration_test.go`)
  y ajoute des lycées de taux et de distances variés autour de (2.5, 48.5).

| Fichier | Couvre |
| ------- | ------ |
| `users_integration_test.go` | Comptes, vérification et changement d'e-mail, révocation de tokens, identités OIDC, `CanMigrate` (rôle en lecture seule), `newUUID`. |
| `zone_integration_test.go` | Établissements de la zone (critères par nature, taux, résultats), communes (score, fiscalité), prix immobiliers, logements sociaux, `IntersectUnions` (union par groupe, intersection entre groupes). |
| `search_integration_test.go` | Vue `etablissement_taux`, `SelectSourceEtablissements` (classement, rayon, critères par nature), `FilterZone` (chaque filtre, valeurs inconnues, prix de la commune à défaut de l'IRIS), cache d'isochrones (TTL, éviction LRU, remplacement), `QuartierProximite`. |
| `primaire_integration_test.go` | Helper `ecoles` (maternelle, élémentaire et écoles « primaire » autour de (2.5, 48.5)) ; migration 019 (reclassement, idempotence), écoles primaires retenues par les critères maternelle et élémentaire (sources, zone, fiche d'un quartier), secteur appliqué, autres natures non concernées. |
| `filieres_integration_test.go` | Helper `filieres` (filières des lycées de `seedZone` et `lycees`) ; migration 020 (idempotence, tableau vide par défaut), lecture de `filieres`, filtre de filières dans les sources, la zone et la fiche d'un quartier. |
| `annuaire_integration_test.go` | Helper `annuaire` (attributs de la migration 021 du lycée A de `seedZone`) ; migration 021 (idempotence), lecture des attributs par `GetEtablissementByUAI`, `FindEtablissementsInZone` et `SelectSourceEtablissements`, valeurs nulles et sections vides (non nil) par défaut. |
| `securite_departement_integration_test.go` | Migration 022 : référentiel `departement` (101 lignes, idempotence), score départemental (rang centile dans le département, commune seule à 50), classement des départements (rang, score moyen, nombre), lecture par `FindCommunesInZone`, commune sans données. |
| `billing_integration_test.go` | Tarifs, débit / remboursement / ajustement, paiements idempotents, journal. |

Toute nouvelle requête ou migration doit être exercée par un de ces tests.
La CI fournit un service PostGIS et définit `DV_TEST_DSN`.
