# Schéma de la base de données

Ce document décrit le schéma de la base de données du projet **Douceville**.

## Vue d'ensemble

- **SGBD** : PostgreSQL avec l'extension **PostGIS** (image Docker `postgis/postgis`).
- **Création du schéma** : les tables sont créées par le **runner de migrations Go** au démarrage du serveur. Il applique, dans l'ordre lexical, les fichiers SQL idempotents embarqués dans le binaire (`//go:embed internal/store/migrations/*.sql`, voir `internal/store/migrate.go`). Il n'y a pas d'ORM.
- **Chaîne de connexion** : `postgres://<user>:<password>@<host>:<port>/<db>` construite dans `internal/config/config.go` à partir des variables `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_DB`.

La base contient **six tables physiques** : `etablissement`, `resultat`, `commune`, `utilisateur`, `email_verification` et `revoked_token`, toutes créées par les migrations Go. La table `commune` est créée vide et chargée par `scripts/import_communes_csv.py`. Les tables `etablissement` et `resultat` sont créées **vides** : il n'existe plus d'ETL dans ce dépôt, les données sont chargées par un moyen externe (dump restauré, chargeur externe). La table `utilisateur` porte désormais l'**authentification native** du backend (voir plus bas).

## Diagramme relationnel

```
┌─────────────────────────┐          ┌──────────────────────────┐
│      etablissement      │          │        resultat          │
├─────────────────────────┤          ├──────────────────────────┤
│ id            PK         │          │ idx               PK     │
│ UAI           UNIQUE  ───┼────┐     │ diplome                  │
│ nom                     │    │     │ annee                    │
│ adresse                 │    │     │ presents                 │
│ lieu_dit                │    └────<│ etablissement_uai  FK    │
│ code_postal             │          │ admis                    │
│ commune                 │          │ mentions                 │
│ code_commune  FK  ──────┼──┐       └──────────────────────────┘
│ position   (POINT)      │  │
│ departement             │  │       UNIQUE (diplome, annee,
│ academie                │  │               etablissement_uai)
│ secteur                 │  │
│ ouverture               │  │       ┌──────────────────────────┐
│ nature                  │  │       │         commune          │
└─────────────────────────┘  │       ├──────────────────────────┤
                             └──────>│ code_commune      PK     │
                                     │ nom                      │
                                     │ date_creation            │
                                     └──────────────────────────┘

Relation : etablissement (1) ──< (N) resultat
  - clé étrangère : resultat.etablissement_uai → etablissement.UAI
  - ON DELETE CASCADE (la suppression d'un établissement supprime ses résultats)
Relation : commune (1) ──< (N) etablissement
  - clé étrangère : etablissement.code_commune → commune.code_commune (NULL autorisé)
```

## Table `etablissement`

Établissement scolaire (lycée, collège, etc.). Créée par `internal/store/migrations/003_etablissement_resultat.sql`.

| Colonne       | Type SQL              | Contraintes                     | Description |
|---------------|-----------------------|---------------------------------|-------------|
| `id`          | `INTEGER`             | **PK**, auto-incrément          | Identifiant interne. |
| `UAI`         | `VARCHAR`             | NOT NULL, **UNIQUE**            | Code UAI (Unité Administrative Immatriculée), identifiant national de l'établissement ; sert de cible de la clé étrangère depuis `resultat`. |
| `nom`         | `VARCHAR`             | NOT NULL                        | Nom de l'établissement. |
| `adresse`     | `VARCHAR`             | NULL                            | Adresse (voie). |
| `lieu_dit`    | `VARCHAR`             | NULL                            | Lieu-dit. |
| `code_postal` | `VARCHAR`             | NOT NULL                        | Code postal. |
| `commune`     | `VARCHAR`             | NOT NULL                        | Commune. |
| `code_commune` | `TEXT`               | NULL, **FK** → `commune.code_commune` | Code INSEE de la commune (`migrations/006_etablissement_code_commune.sql`, indexé). Les arrondissements de Paris/Marseille/Lyon sont ramenés à la commune parente ; NULL si la commune est absente de `commune` (collectivités d'outre-mer). |
| `position`    | `geometry(Point)` (SRID 0) | NOT NULL                   | Position géographique (PostGIS), SRID 0 pour rester cohérent avec les isochrones (`ST_GeomFromEWKT` sans SRID). Index GiST. Exposée en GeoJSON côté API via `ST_AsGeoJSON`. |
| `departement` | `VARCHAR`             | NOT NULL                        | Code / nom du département. |
| `academie`    | `VARCHAR`             | NULL                            | Académie de rattachement. |
| `secteur`     | `VARCHAR`             | NOT NULL                        | Secteur (`public` / `privé` (sous contrat) / `hors contrat`). |
| `ouverture`   | `TIMESTAMP`           | NULL                            | Date d'ouverture. |
| `nature`      | `VARCHAR`             | NOT NULL                        | Nature de l'établissement (ex. `lycée`, `collège`). |

**Relations** : un établissement a plusieurs `resultat` (FK `resultat.etablissement_uai` → `etablissement.UAI`, suppression en cascade) et appartient à une `commune` (FK `etablissement.code_commune` → `commune.code_commune`).

### Détails sur `position`
La colonne est de type `geometry(Point)` (SRID 0). En lecture, l'API la renvoie en GeoJSON via `ST_AsGeoJSON(position)` (voir `etabColumns` dans `internal/store/queries.go`). Les données doivent être insérées en SRID 0 (ex. `ST_GeomFromText('POINT(1.4401531 43.6085909)')`) pour que `ST_Within` fonctionne avec les isochrones.

## Table `resultat`

Résultat d'un examen pour un établissement et une année donnés. Créée par `internal/store/migrations/003_etablissement_resultat.sql`.

| Colonne             | Type SQL   | Contraintes                              | Description |
|---------------------|------------|------------------------------------------|-------------|
| `idx`               | `INTEGER`  | **PK**, auto-incrément                   | Identifiant interne. |
| `diplome`           | `VARCHAR`  | NOT NULL                                 | Diplôme concerné (ex. `Brevet`, `Baccalauréat`). |
| `annee`             | `INTEGER`  | NOT NULL                                 | Année de l'examen. |
| `presents`          | `INTEGER`  | NULL                                     | Nombre de candidats présents. |
| `admis`             | `INTEGER`  | NULL                                     | Nombre d'admis. |
| `mentions`          | `INTEGER`  | NULL                                     | Nombre de mentions. |
| `etablissement_uai` | `VARCHAR`  | **FK** → `etablissement.UAI`, ON DELETE CASCADE | Établissement rattaché (par son code UAI). |

**Contrainte d'unicité** : `UNIQUE (diplome, annee, etablissement_uai)` — un seul résultat par couple (diplôme, année) et par établissement.

## Table `commune`

Référentiel des communes actuelles (Code officiel géographique INSEE, fichier `v_commune_depuis_1943.csv`). Créée par `internal/store/migrations/005_commune.sql`, chargée par `scripts/import_communes_csv.py` : seules les lignes sans `DATE_FIN` et de type `COM` sont conservées (les arrondissements municipaux `ARM` sont exclus).

| Colonne         | Type SQL | Contraintes | Description |
|-----------------|----------|-------------|-------------|
| `code_commune`  | `TEXT`   | **PK** (unique, NOT NULL) | Code INSEE de la commune (colonne `COM`, ex. `01001`, `2A004`) ; cible de la FK `etablissement.code_commune`. |
| `nom`           | `TEXT`   | NOT NULL    | Nom en typographie riche (colonne `NCCENR`). |
| `date_creation` | `DATE`   | NOT NULL    | Début de validité de la commune (colonne `DATE_DEBUT`). |

## Table `utilisateur`

Profil et identifiants des utilisateurs, support de l'**authentification native**
du backend Go (plus de Supabase). À l'inscription, le backend génère un UUID v4
(`id`), hache le mot de passe (PBKDF2-SHA256, `internal/auth/password.go`) et émet
un JWT HS256 (`aud=douceville`) signé avec `JWT_SECRET`. À chaque requête protégée,
le middleware valide le JWT puis récupère le profil par `id` (claim `sub`).
Colonnes créées par `migrations/001_utilisateur.sql`, `migrations/002_auth.sql` et
`migrations/004_email_verification_logout.sql`.

| Colonne         | Type SQL   | Contraintes                    | Description |
|-----------------|------------|--------------------------------|-------------|
| `id`            | `TEXT`     | **PK**                         | UUID v4 généré à l'inscription ; alimente le claim `sub` du JWT. |
| `email`         | `TEXT`     | **UNIQUE** (`lower(email)`)    | Adresse e-mail (identifiant de connexion). |
| `password_hash` | `TEXT`     | NULL                           | Hash PBKDF2-SHA256 salé (`pbkdf2_sha256$iter$salt$hash`). |
| `first_name`    | `TEXT`     | NULL                           | Prénom. |
| `last_name`     | `TEXT`     | NULL                           | Nom. |
| `avatar_url`    | `TEXT`     | NULL                           | URL de l'avatar. |
| `permissions`   | `TEXT[]`   | NOT NULL, défaut `'{}'`        | Liste des permissions. |
| `perms`         | `user_perms` | NOT NULL, défaut `'unverified'` | Niveau d'accès, enum ordonné `unverified` < `verified` < `granted` < `admin`. Seuls `granted` et `admin` accèdent à l'application. Les comptes antérieurs à la migration 004 sont passés à `granted`. |

## Table `email_verification`

Jetons de vérification d'adresse e-mail (`migrations/004_email_verification_logout.sql`).
Seul le hash du jeton est stocké ; un jeton est à usage unique et un nouvel envoi
remplace les jetons précédents de l'utilisateur.

| Colonne      | Type SQL      | Contraintes                         | Description |
|--------------|---------------|-------------------------------------|-------------|
| `token_hash` | `TEXT`        | **PK**                              | SHA-256 (hex) du jeton envoyé par e-mail. |
| `user_id`    | `TEXT`        | NOT NULL, FK `utilisateur(id)` ON DELETE CASCADE | Utilisateur à vérifier. |
| `expires_at` | `TIMESTAMPTZ` | NOT NULL                            | Expiration du lien (24 h). |

## Table `revoked_token`

JWT révoqués par déconnexion (`migrations/004_email_verification_logout.sql`). Les
lignes expirées sont purgées à chaque nouvelle déconnexion.

| Colonne      | Type SQL      | Contraintes | Description |
|--------------|---------------|-------------|-------------|
| `jti`        | `TEXT`        | **PK**      | Claim `jti` du token révoqué. |
| `expires_at` | `TIMESTAMPTZ` | NOT NULL    | Expiration du token (au-delà, la ligne est inutile). |

## Objets non persistés (hors base)

Structures Go d'échange API (`internal/models/models.go`), sans table associée :

- `DvUser` / `UserRecord` — profil public / interne (hash du mot de passe, jamais sérialisé).
- `Credentials`, `AuthResponse` — corps des requêtes `/api/auth/*` et réponse `{token, user}`.
- `Etablissement`, `Resultat` — projections lues depuis la base.
- `Isochrone` — zone d'accessibilité (géométrie `POLYGON`) calculée à la volée, non stockée.
- `ListeEtablissementsAvecIsochrone`, `QueryParameters` — réponse/requête de `POST /api/etablissements`.

## Références dans le code

- Migrations SQL : `internal/store/migrations/*.sql` (runner : `internal/store/migrate.go`).
- Accès base : `internal/store/queries.go` ; modèles : `internal/models/models.go`.
- Auth : `internal/auth/` (JWT `jwt.go`, mots de passe `password.go`, middleware `middleware.go`).
- Infrastructure PostGIS : `docker-compose.yml`, `docker-compose.dev.yml`.
