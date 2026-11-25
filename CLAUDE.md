# CLAUDE.md

Consignes pour les agents de code (Claude Code…) travaillant sur ce dépôt.

## Le projet en bref

Douceville est un service web monolithique en Go : un seul binaire sert l'API
JSON (`/api`) et l'interface web statique (`/`, embarquée depuis `web/`). Il
calcule la zone atteignable depuis un ou plusieurs points de départ
(isochrones OpenRouteService, géocodage BAN) et affiche les établissements
scolaires, la sécurité, la fiscalité, les prix immobiliers et les logements
sociaux de cette zone. Données dans PostgreSQL + PostGIS.

Vue d'ensemble, API et configuration : [`README.md`](README.md). Schéma de
base : [`schema.md`](schema.md). Rôle de chaque paquet : `internal/*/README.md`.

## Commandes

```bash
go build ./...                       # compilation
go vet ./...                         # analyse statique
go test ./...                        # tests unitaires (sans base ni réseau)
DV_TEST_DSN=postgres://user:pass@localhost:5432/postgres go test -cover ./...   # + intégration PostGIS
go test -coverprofile=/tmp/cov.out ./... && go tool cover -func=/tmp/cov.out    # couverture par fonction
./restart.sh                         # (re)lance PostGIS en local (docker compose)
./launch_server.sh                   # lance le serveur avec les variables de .env
```

Pour les tests d'intégration, une base PostGIS jetable suffit (chaque test y
crée puis supprime sa propre base ; le rôle doit avoir le droit `CREATEDB`) :

```bash
docker run -d --rm --name dv-test-pg -e POSTGRES_USER=test -e POSTGRES_PASSWORD=test \
  -p 127.0.0.1:55432:5432 postgis/postgis:18-3.7
DV_TEST_DSN=postgres://test:test@127.0.0.1:55432/test go test ./...
```

Ne jamais faire pointer `DV_TEST_DSN` vers une base de production.

Avant de considérer une tâche terminée : `go build ./...`, `go vet ./...` et
`go test ./...` (avec `DV_TEST_DSN` si `internal/store` est touché) doivent
passer. La CI (`.github/workflows/docker-image.yml`) les exécute à chaque push,
avec un service PostGIS, puis construit l'image Docker ; l'image n'est publiée
sur GHCR que pour un tag `v*`.

## Carte du code

| Chemin | Rôle |
| ------ | ---- |
| `cmd/server/main.go` | Assemblage : config → base → migrations → dépendances → routeur → serveur HTTP. |
| `internal/api` | Routeur chi, handlers HTTP, facturation des requêtes, service de l'UI. |
| `internal/auth` | JWT, middleware Bearer, mots de passe PBKDF2, jetons de vérification, OIDC. |
| `internal/billing` | Interface `PaymentProvider` et client Mollie. |
| `internal/config` | Variables d'environnement → `config.Config` (avec validation). |
| `internal/geo` | Clients OpenRouteService (isochrones) et addok/BAN (géocodage). |
| `internal/mail` | Interface `Sender` : SMTP ou journalisation. |
| `internal/models` | Structures JSON échangées par l'API et entre paquets. |
| `internal/store` | Interface `Store`, implémentation pgx `PgStore`, migrations SQL embarquées. |
| `web/` | Frontend HTML/CSS/JS sans build (Leaflet), embarqué via `//go:embed`. |
| `scripts/` | Scripts Python (`uv run`) d'import de données et d'administration. |

Sens des dépendances : `cmd/server` → `api` → (`auth`, `billing`, `geo`,
`mail`, `store`) → `models`. `models` ne dépend d'aucun autre paquet interne ;
`config` n'est utilisé que par `cmd/server`.

## Conventions

- **Langue** : commentaires, messages de commit, documentation et textes de
  l'interface en **français**. Les messages de journal (`slog`) et les messages
  d'erreur techniques de l'API (`detail`) sont en anglais ; les docstrings des
  scripts Python aussi.
- **Commentaires** : chaque symbole exporté a un commentaire de doc en français
  commençant par son nom (`// Store définit…`).
- **Dépendances injectées par interfaces** : les handlers (`api.Handlers`)
  dépendent d'interfaces (`store.Store`, `Isochroner`, `RoutePlanner`, `Geocoder`,
  `AddressSuggester`, `OIDCProvider`, `billing.PaymentProvider`, `mail.Sender`)
  pour être testables sans base ni réseau. Une nouvelle dépendance externe suit
  le même schéma.
- **Base de données** : pas d'ORM, SQL écrit à la main avec pgx. Toute
  évolution du schéma passe par une **nouvelle migration**
  `internal/store/migrations/NNN_nom.sql`, **idempotente** (`IF NOT EXISTS`…),
  car toutes les migrations sont rejouées à chaque démarrage, dans l'ordre
  lexical, sans table de suivi. Ne jamais modifier une migration déjà publiée.
- **Ajout d'une méthode au `Store`** : la déclarer dans l'interface
  (`internal/store/store.go`), l'implémenter dans `PgStore`, la tester en
  intégration (`internal/store/*_integration_test.go`), et l'ajouter aux
  doublures de test (`mockStore` dans `internal/api/handlers_test.go`,
  `fakeBill` si elle touche la facturation, `failingStore` si un handler doit
  gérer son échec).
- **Réponses HTTP** : `writeJSON` / `writeError` ; les erreurs ont la forme
  `{"detail": "...", "code": "..."}`. Les codes métier (`account_not_granted`,
  `insufficient_credit`…) sont des constantes et sont exploités par le frontend.
- **Journaux** : `logger(r.Context())` (enrichi du `request_id`), niveau `Warn`
  pour les erreurs client, `Error` pour les erreurs serveur.
- **Requêtes payantes** : appeler `h.charge(...)` **avant** les appels externes
  et `refund()` en cas d'échec ; les administrateurs ne sont jamais facturés.
- **Sécurité** : le niveau d'accès est relu en base à chaque requête ; seuls des
  hash de jetons (SHA-256) et de mots de passe (PBKDF2) sont stockés ; ne jamais
  sérialiser `models.UserRecord` vers le client.
- **Frontend** : JS natif sans framework ni build. Toute modification de `web/`
  impose de recompiler le binaire. L'état de la page est reflété dans l'URL
  (`syncUrl` / `applyQuery` dans `app.js`) : un nouveau filtre ou une nouvelle
  couche doit y être ajouté.
- **Couplages à surveiller** :
  - les poids des indicateurs de sécurité sont dupliqués entre
    `internal/store/migrations/009_securite_score.sql` et `SECURITY_INDICATORS`
    dans `web/app.js` ;
  - la définition du taux de réussite (Σadmis / Σprésents) est dupliquée entre
    la vue `etablissement_taux` (`migrations/017_etablissement_taux.sql`) et
    `successRate()` dans `web/app.js` ;
  - les natures, secteurs et modes de transport acceptés (`models.Natures`,
    `Secteurs`, `Transports`) doivent correspondre à `CRITERE_NATURES`,
    `SECTEURS` et au gabarit `critere-tpl` du frontend ; la limite de sources
    par nature (`sourcesParNature` dans `internal/api/search.go`) à
    `SOURCES_PAR_NATURE` dans `web/app.js` ;
  - la nature `Primaire` (nom contenant « primaire ») est attribuée à la fois
    par `scripts/import_etablissements_parquet.py` et par
    `migrations/019_etablissement_primaire.sql` ; les natures de critère
    qu'elle satisfait (`models.NaturesPrimaire`) sont utilisées par
    `critereMatch` (`internal/store/search.go`), et sa couleur est dans
    `NATURE_GROUPS` (`web/app.js`).
- **Piège frontend** : `web/app.js` est un script classique exécuté de haut en
  bas ; un appel de niveau supérieur ne doit pas utiliser une variable
  `let`/`const` déclarée plus bas (erreur « Cannot access … before
  initialization » qui interrompt tout le script).
- **Secrets** : ne jamais committer `.env` ni de clé (Mollie, ORS, OIDC…) ;
  `sample.env` ne contient que des valeurs d'exemple.

## Tests : règle obligatoire

**Chaque évolution doit être testée autant que possible, dans le même commit
que le code.** Une fonctionnalité, une route, une requête SQL ou une correction
de bug sans test n'est pas terminée.

- **Correction de bug** : écrire d'abord un test qui reproduit le bug (il doit
  échouer), puis corriger.
- **Cas à couvrir** : le cas nominal, les erreurs de validation (`400`/`422`),
  les droits (`401`/`403`, niveaux `granted`/`admin`), les erreurs des
  dépendances (base, services externes → `500`/`502`) et les cas limites
  (listes vides, valeurs nulles, doublons, idempotence).
- **Où tester** :
  - handlers (`internal/api`) : à travers le routeur avec `call` / `callAs`,
    les doublures `mockStore`, `fakeBill`, `fakeProvider`, `mockIso`, `mockRoute`,
    `mockGeo`, `mockSuggest`, `fakeMailer` ; `failingStore`
    (`failures_test.go`) fait échouer une méthode précise du `Store` ;
  - requêtes SQL (`internal/store`) : tests d'intégration sur une vraie base
    via `newTestStore(t)` (base jetable, migrations appliquées) et `seedZone`
    pour les données spatiales ; une nouvelle requête ou migration doit y être
    exercée ;
  - clients HTTP externes (`geo`, `billing`, `auth/oidc`) : serveurs
    `httptest` ; `mail` : serveur SMTP factice (`smtp_test.go`) ;
  - logique pure : tests table-driven.
- **Pas de réseau ni de service réel** dans `go test ./...` : tout appel
  externe passe par une interface ou un serveur `httptest`. Seuls les tests
  `internal/store` utilisent une base, et sont ignorés sans `DV_TEST_DSN`.
- **Couverture** : ne pas la faire baisser. Repères actuels (avec
  `DV_TEST_DSN`) : ~87 % au total, ≥ 80 % pour chaque paquet de `internal/`.
  Vérifier avec `go tool cover -func` que le code ajouté est couvert.
- Les tests suivent les conventions du dépôt : commentaires en français,
  messages d'échec explicites (`got … want …`), helpers marqués `t.Helper()`.
- Le frontend (`web/app.js`) n'a pas de tests automatisés : vérifier ses
  évolutions manuellement dans le navigateur et le signaler.

## Documentation : règle de mise à jour obligatoire

La documentation fait partie du livrable. **À chaque évolution du code, les
fichiers Markdown concernés doivent être mis à jour dans le même commit** :

| Fichier | À mettre à jour quand… |
| ------- | ---------------------- |
| `README.md` | une fonctionnalité, une route d'API, une variable d'environnement, une commande, un script ou une procédure (lancement, import, déploiement) change. |
| `CLAUDE.md` | une convention, une commande, l'organisation du code ou un piège à connaître change. |
| `schema.md` | une migration ajoute ou modifie une table, une colonne, un index ou une vue. |
| `internal/<paquet>/README.md` | le rôle, les types ou fonctions principaux, les dépendances, les invariants ou les tests (fichiers, doublures, helpers) d'un paquet changent. |
| `sample.env` | une variable d'environnement est ajoutée, renommée ou supprimée. |

Règles :

- Tout **nouveau paquet** sous `internal/` doit être créé avec son
  `README.md`, sur le modèle des existants (rôle, contenu, dépendances, points
  d'attention, tests), et être ajouté aux tableaux de `README.md` et de
  `CLAUDE.md`.
- Un paquet supprimé ou renommé entraîne la mise à jour des liens dans
  `README.md` et `CLAUDE.md`.
- Toute la documentation est rédigée en **français**.
- Avant de terminer une tâche, relire les Markdown listés ci-dessus et vérifier
  qu'aucun n'est devenu inexact (routes, variables, noms de fichiers, valeurs
  par défaut).
