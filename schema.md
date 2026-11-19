# Schéma de la base de données

Ce document décrit le schéma de la base de données du projet **Douceville**.

## Vue d'ensemble

- **SGBD** : PostgreSQL avec l'extension **PostGIS** (image Docker `postgis/postgis`).
- **Création du schéma** : les tables sont créées par le **runner de migrations Go** au démarrage du serveur. Il applique, dans l'ordre lexical, les fichiers SQL idempotents embarqués dans le binaire (`//go:embed internal/store/migrations/*.sql`, voir `internal/store/migrate.go`). Il n'y a pas d'ORM.
- **Chaîne de connexion** : `postgres://<user>:<password>@<host>:<port>/<db>` construite dans `internal/config/config.go` à partir des variables `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_DB`.

La base contient **neuf tables physiques** : `etablissement`, `resultat`, `commune`, `quartier`, `immobilier`, `securite`, `utilisateur`, `email_verification` et `revoked_token`, toutes créées par les migrations Go, ainsi qu'une **vue matérialisée** `securite_score`. Les tables `commune` et `securite` sont créées vides et chargées par `scripts/import_communes_csv.py` et `scripts/import_securite_parquet.py` ; les contours des communes (`commune.contour`) sont chargés par `scripts/import_contours_communes.py` et les quartiers IRIS (`quartier`) par `scripts/import_contours_iris.py` ; la table `immobilier` (ventes DVF géolocalisées, rattachées à un IRIS) est chargée par `scripts/import_immobilier_dvf.py`. Les tables `etablissement` et `resultat` sont créées **vides** : il n'existe plus d'ETL dans ce dépôt, les données sont chargées par un moyen externe (dump restauré, chargeur externe). La table `utilisateur` porte désormais l'**authentification native** du backend (voir plus bas).

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
                                     │ contour (MULTIPOLYGON)   │
                                     └──────────────────────────┘
                                                  ^
                                     ┌────────────┼─────────────┐
                                     │         quartier         │
                                     ├──────────────────────────┤
                                     │ iris              PK     │
                                     │ code_commune      FK     │
                                     │ nom_iris                 │
                                     │ type_iris                │
                                     │ contour (MULTIPOLYGON)   │
                                     └──────────────────────────┘
                                                  ^
                                     ┌────────────┼─────────────┐
                                     │        immobilier        │
                                     ├──────────────────────────┤
                                     │ iris              FK     │
                                     │ date_mutation            │
                                     │ nature_mutation          │
                                     │ valeur_fonciere          │
                                     │ id_parcelle              │
                                     │ surface_terrain          │
                                     │ surface_reelle_bati      │
                                     └──────────────────────────┘

Relation : etablissement (1) ──< (N) resultat
  - clé étrangère : resultat.etablissement_uai → etablissement.UAI
  - ON DELETE CASCADE (la suppression d'un établissement supprime ses résultats)
Relation : commune (1) ──< (N) etablissement
  - clé étrangère : etablissement.code_commune → commune.code_commune (NULL autorisé)
Relation : commune (1) ──< (N) securite
  - clé étrangère : securite.code_commune → commune.code_commune
Relation : commune (1) ──< (N) quartier
  - clé étrangère : quartier.code_commune → commune.code_commune
Relation : quartier (1) ──< (N) immobilier
  - clé étrangère : immobilier.iris → quartier.iris
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
| `code_commune`  | `TEXT`   | **PK** (unique, NOT NULL) | Code INSEE de la commune (colonne `COM`, ex. `01001`, `2A004`) ; cible des FK `etablissement.code_commune` et `securite.code_commune`. |
| `nom`           | `TEXT`   | NOT NULL    | Nom en typographie riche (colonne `NCCENR`). |
| `date_creation` | `DATE`   | NOT NULL    | Début de validité de la commune (colonne `DATE_DEBUT`). |
| `contour`       | `geometry(MultiPolygon, 4326)` | NULL | Contour de la commune (WGS84), ajouté par `migrations/008_commune_contour.sql` et chargé par `scripts/import_contours_communes.py` depuis les « contours administratifs » Etalab (`communes-50m.geojson.gz`, même millésime COG). Index GIST `commune_contour_gist`. Sert à trouver les communes touchées par une isochrone (`ST_Intersects`). |

## Table `quartier`

Quartiers IRIS (« Ilots Regroupés pour l'Information Statistique », IGN « contours IRIS », couche WFS `STATISTICALUNITS.IRIS:contours_iris` de la Géoplateforme). Créée par `internal/store/migrations/010_quartier.sql`, chargée par `scripts/import_contours_iris.py` : sans fichier en argument, le script télécharge toutes les entités du WFS par pages de 5 000 (un export WFS unique est tronqué à 5 000 entités). Les IRIS des arrondissements de Paris/Marseille/Lyon sont rattachés à la commune parente ; les IRIS dont la commune est absente de `commune` sont ignorés. Import en upsert sur `iris`.

| Colonne        | Type SQL                       | Contraintes | Description |
|----------------|--------------------------------|-------------|-------------|
| `iris`         | `TEXT`                         | **PK** (unique, NOT NULL) | Code IRIS complet sur 9 caractères (propriété `code_iris` = code INSEE + n° IRIS, ex. `670430101`). Le n° IRIS seul (propriété `iris`, 4 chiffres) n'est pas unique. |
| `code_commune` | `TEXT`                         | NOT NULL, **FK** → `commune.code_commune` | Code INSEE de la commune (propriété `code_insee`, arrondissements ramenés à la commune parente). Indexé (`quartier_code_commune_idx`). |
| `nom_iris`     | `TEXT`                         | NOT NULL    | Nom de l'IRIS. |
| `type_iris`    | `TEXT`                         | NOT NULL    | Type d'IRIS : `H` habitat, `A` activité, `D` divers, `Z` commune non découpée. |
| `contour`      | `geometry(MultiPolygon, 4326)` | NOT NULL    | Contour de l'IRIS (WGS84). Index GIST `quartier_contour_gist`. |

## Table `immobilier`

Ventes immobilières DVF (fichier CSV gzippé « demandes de valeurs foncières géolocalisées » de data.gouv.fr), une ligne par vente d'un seul bien bâti. Créée par `internal/store/migrations/011_immobilier.sql` (qui supprime l'ancienne version de la table, prix au m² agrégés par IRIS, si elle existe encore), chargée par `scripts/import_immobilier_dvf.py`.

Filtres à l'import : `nature_mutation = 'Vente'`, `surface_reelle_bati > 0`, `valeur_fonciere`, `longitude` et `latitude` renseignées. Une vente (`id_mutation`) comportant plusieurs lignes bâties est ignorée en entier, car chaque ligne y répète le prix total de la vente (les dépendances, de surface bâtie nulle, sont déjà écartées). Le quartier est celui dont `quartier.contour` contient le point (`longitude`, `latitude`) ; les ventes hors de tout quartier sont ignorées. L'import remplace toute la table (option `--start-year YEAR` : seules les ventes datées de `YEAR` ou après sont lues et remplacées), en une seule transaction.

| Colonne               | Type SQL         | Contraintes | Description |
|-----------------------|------------------|-------------|-------------|
| `iris`                | `TEXT`           | NOT NULL, **FK** → `quartier.iris` | Quartier IRIS contenant le point géolocalisé de la vente. |
| `date_mutation`       | `DATE`           | NOT NULL    | Date de la vente. |
| `nature_mutation`     | `TEXT`           | NOT NULL    | Toujours `Vente` (filtré à l'import). |
| `valeur_fonciere`     | `NUMERIC(15,2)`  | NOT NULL    | Prix de vente (€). |
| `id_parcelle`         | `TEXT`           | NOT NULL    | Identifiant cadastral de la parcelle (14 caractères). |
| `surface_terrain`     | `INTEGER`        | NULL        | Surface du terrain (m²), NULL si absente. |
| `surface_reelle_bati` | `INTEGER`        | NOT NULL    | Surface réelle bâtie (m²), > 0. |

**Index** : `immobilier_iris_date_idx (iris, date_mutation)` — sert aussi d'index pour la FK. Pas de clé primaire (la source n'en fournit pas une fois `id_mutation` écarté).

L'API (`FindImmobilierInZone`) calcule le prix au m² médian (`valeur_fonciere / surface_reelle_bati`) par IRIS et par commune sur toutes les ventes de la table.

## Table `securite`

Délinquance enregistrée par la police et la gendarmerie, par commune, année et indicateur (SSMSI, « base statistique communale de la délinquance », fichier Parquet de data.gouv.fr). Créée par `internal/store/migrations/007_securite.sql`, chargée par `scripts/import_securite_parquet.py`. Les lignes des arrondissements de Paris/Lyon/Marseille sont ignorées (la commune parente figure déjà dans le fichier).

| Colonne           | Type SQL           | Contraintes | Description |
|-------------------|--------------------|-------------|-------------|
| `code_commune`    | `TEXT`             | NOT NULL, **FK** → `commune.code_commune` | Code INSEE de la commune (colonne `CODGEO_2026`). |
| `annee`           | `INTEGER`          | NOT NULL    | Année des faits. |
| `indicateur`      | `TEXT`             | NOT NULL    | Indicateur (ex. `Cambriolages de logement`, `Violences sexuelles`). |
| `unite_de_compte` | `TEXT`             | NOT NULL    | Unité comptée (`Victime`, `Infraction`, `Véhicule`, `Mis en cause`, `Victime entendue`). |
| `nombre`          | `INTEGER`          | NULL        | Nombre de faits ; NULL si non diffusé. |
| `taux_pour_mille` | `DOUBLE PRECISION` | NULL        | Taux pour 1 000 habitants (ou logements) ; NULL si non diffusé. |
| `est_diffuse`     | `BOOLEAN`          | NOT NULL    | `true` si la donnée est diffusée (`diff`), `false` sinon (`ndiff`, secret statistique). |

**Clé primaire** : `(code_commune, annee, indicateur)` — elle sert aussi d'index pour la FK.

## Vue matérialisée `securite_score`

Score de sécurité par commune et par année, de 0 (moins sûre) à 100 (plus sûre). Créée par `internal/store/migrations/009_securite_score.sql` ; **rafraîchie** (`REFRESH MATERIALIZED VIEW securite_score`) à la fin de `scripts/import_securite_parquet.py`.

Calcul :
1. pour chaque (année, indicateur), rang centile du `taux_pour_mille` de la commune parmi les communes ayant **diffusé** cet indicateur ; les ex aequo (très nombreux à 0) reçoivent le rang moyen `(percent_rank + cume_dist) / 2` ;
2. indice composite = moyenne pondérée des rangs sur les seuls indicateurs diffusés (poids 3 : violences physiques et sexuelles, vols avec armes, vols violents sans arme, cambriolages ; poids 2 : destructions et dégradations, vols liés aux véhicules, vols sans violence ; poids 1 : escroqueries, stupéfiants) ;
3. score = rang centile inversé de l'indice parmi toutes les communes de l'année × 100 : un score de 70 signifie « plus sûre que 70 % des communes ».

Une commune sans aucun indicateur diffusé est absente de la vue (score inconnu, affiché en gris).

| Colonne          | Type SQL  | Description |
|------------------|-----------|-------------|
| `code_commune`   | `TEXT`    | Code INSEE de la commune. |
| `annee`          | `INTEGER` | Année des faits. |
| `score`          | `INTEGER` | Score 0–100 (100 = plus sûre). |
| `nb_indicateurs` | `INTEGER` | Nombre d'indicateurs diffusés pris en compte (sur 15). |

**Index unique** : `(code_commune, annee)`.

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
- `CommuneSecurite` — commune touchée par l'isochrone : code, nom, `score` / `annee` / `nb_indicateurs` (dernière année de `securite_score`, `null` sans données) et `contour` GeoJSON ; renvoyée dans le champ `communes` de `POST /api/etablissements`.
- `ListeEtablissementsAvecIsochrone`, `QueryParameters` — réponse/requête de `POST /api/etablissements`.

## Références dans le code

- Migrations SQL : `internal/store/migrations/*.sql` (runner : `internal/store/migrate.go`).
- Accès base : `internal/store/queries.go` ; modèles : `internal/models/models.go`.
- Auth : `internal/auth/` (JWT `jwt.go`, mots de passe `password.go`, middleware `middleware.go`).
- Infrastructure PostGIS : `docker-compose.yml`, `docker-compose.dev.yml`.
