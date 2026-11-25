# Schéma de la base de données

Ce document décrit le schéma de la base de données du projet **Douceville**.

## Vue d'ensemble

- **SGBD** : PostgreSQL avec l'extension **PostGIS** (image Docker `postgis/postgis`).
- **Création du schéma** : les tables sont créées par le **runner de migrations Go** au démarrage du serveur. Il applique, dans l'ordre lexical, les fichiers SQL idempotents embarqués dans le binaire (`//go:embed internal/store/migrations/*.sql`, voir `internal/store/migrate.go`). Il n'y a pas d'ORM.
- **Chaîne de connexion** : `postgres://<user>:<password>@<host>:<port>/<db>` construite dans `internal/config/config.go` à partir des variables `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_DB`.

La base contient **seize tables physiques**, une **vue matérialisée** (`securite_score`) et une **vue** (`etablissement_taux`), toutes créées par les migrations Go :

- **Données géographiques et scolaires** (créées vides, chargées par les scripts `scripts/import_*.py`, enchaînés par `import.sh`) : `commune` (`import_communes_csv.py`, contours par `import_contours_communes.py`), `quartier` (IRIS, `import_contours_iris.py`), `etablissement` (`import_etablissements_parquet.py`), `resultat` (`import_resultats_parquet.py`), `securite` (`import_securite_parquet.py`), `fiscalite` (`import_fiscalite_parquet.py`), `immobilier` (ventes DVF, `import_immobilier_dvf.py`) et `logement_social` (RPLS, `import_logement_social.py`).
- **Comptes et authentification** (gérées par le backend) : `utilisateur`, `email_verification`, `revoked_token` et `oidc_identity`.
- **Facturation** (gérées par le backend) : `billing_settings`, `credit_transaction` et `payment`.
- **Cache** (géré par le backend) : `isochrone_cache` (isochrones OpenRouteService des recherches).

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
│ filieres   (TEXT[])     │  │
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

                                     ┌──────────────────────────┐
                                     │     logement_social      │
                                     ├──────────────────────────┤
                                     │ iris (→ quartier) FK     │
                                     │ type_droit_immobilier    │
                                     │ type_construction        │
                                     │ nb_pieces                │
                                     │ surface                  │
                                     │ annee_construction       │
                                     │ position   (POINT)       │
                                     └──────────────────────────┘

Relation : etablissement (1) ──< (N) resultat
  - clé étrangère : resultat.etablissement_uai → etablissement.UAI
  - ON DELETE CASCADE (la suppression d'un établissement supprime ses résultats)
Relation : commune (1) ──< (N) etablissement
  - clé étrangère : etablissement.code_commune → commune.code_commune (NULL autorisé)
Relation : commune (1) ──< (N) securite
  - clé étrangère : securite.code_commune → commune.code_commune
Relation : commune (1) ──< (N) fiscalite
  - clé étrangère : fiscalite.code_commune → commune.code_commune
Relation : commune (1) ──< (N) quartier
  - clé étrangère : quartier.code_commune → commune.code_commune
Relation : quartier (1) ──< (N) immobilier
  - clé étrangère : immobilier.iris → quartier.iris
Relation : quartier (1) ──< (N) logement_social
  - clé étrangère : logement_social.iris → quartier.iris
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
| `nature`      | `VARCHAR`             | NOT NULL                        | Nature de l'établissement : `Maternelle`, `Elementaire`, `Primaire`, `Collège` ou `Lycée`. `Primaire` (nom contenant « primaire », sans distinction de casse : à l'import et par `migrations/019_etablissement_primaire.sql`, qui reclasse les établissements déjà importés) satisfait les critères de recherche `Maternelle` et `Elementaire` (`critereMatch` dans `internal/store/search.go`). |
| `filieres`    | `TEXT[]`              | NOT NULL, défaut `'{}'`         | Filières d'un lycée, plusieurs possibles : `Général`, `Technologique`, `Professionnel`, `Agricole` (`migrations/020_etablissement_filieres.sql`) ; vide pour les autres natures. Déduites à l'import du code natureUAI (LEGT → général + technologique, polyvalent → les trois, LP / SEP → professionnel…) et du ministère de tutelle (Agriculture → `Agricole`). La migration ne les remplit pas : réimporter les établissements. Un critère `Lycée` avec filières retient les lycées ayant au moins une filière en commun (`critereMatch`). |

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

## Vue `etablissement_taux`

Taux de réussite aux examens de chaque établissement, en % (`internal/store/migrations/017_etablissement_taux.sql`) : `100 × Σadmis / Σpresents` sur tous ses résultats dont `presents > 0` et `admis` est renseigné. Un établissement sans tel résultat (maternelles, élémentaires…) est absent de la vue : son taux est inconnu et il n'est jamais exclu par un filtre de taux. Même définition que `successRate()` dans `web/app.js` (à garder synchronisées).

| Colonne | Type SQL           | Description |
|---------|--------------------|-------------|
| `uai`   | `TEXT`             | Code UAI de l'établissement (`resultat.etablissement_uai`). |
| `taux`  | `DOUBLE PRECISION` | Taux de réussite, 0–100. |

Elle sert au choix des établissements sources de la recherche (`SelectSourceEtablissements` : 10 meilleurs taux par nature dans le rayon), au filtrage des établissements de la zone (`FindEtablissementsInZone`) et à la fiche d'un quartier (`QuartierProximite`).

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

L'API (`FindImmobilierInZone`, `FilterZone`, `QuartierProximite`) calcule le prix au m² médian (`valeur_fonciere / surface_reelle_bati`) par IRIS et par commune sur toutes les ventes de la table. Le filtre de prix de la recherche (`FilterZone`) utilise le prix de l'IRIS, ou celui de sa commune si l'IRIS n'a aucune vente.

## Table `logement_social`

Logements locatifs des bailleurs sociaux (RPLS, fichier CSV « données détaillées au logement du répertoire des logements locatifs des bailleurs sociaux » de data.gouv.fr, séparateur `;`), une ligne par logement. Créée par `internal/store/migrations/013_logement_social.sql`, chargée par `scripts/import_logement_social.py`.

Filtres à l'import : les lignes dont `PLG_IRIS2024_CODE` est vide ou contient autre chose que des chiffres (IRIS fictifs `…XXXX`, Corse `2A…`/`2B…`) sont ignorées, ainsi que celles dont l'IRIS est absent de `quartier`. L'import remplace toute la table, en une seule transaction.

| Colonne                 | Type SQL           | Contraintes | Description |
|-------------------------|--------------------|-------------|-------------|
| `iris`                  | `TEXT`             | NOT NULL, **FK** → `quartier.iris` | Code IRIS 2024 du logement (`PLG_IRIS2024_CODE`). |
| `type_droit_immobilier` | `TEXT`             | NULL        | Droit réel du bailleur (`DROIT_LIBELLE` : pleine propriété, usufruit, bail emphytéotique, …). |
| `type_construction`     | `TEXT`             | NULL        | `TYPECONST_LIBELLE` (collectif, individuel, logement étudiant) ; NULL si `NC`. |
| `nb_pieces`             | `INTEGER`          | NULL        | Nombre de pièces (`NBPIECE`) ; NULL si `NC`. |
| `surface`               | `INTEGER`          | NULL        | Surface habitable en m² (`SURFHAB`) ; NULL si absente ou de plus de 4 chiffres. |
| `annee_construction`    | `INTEGER`          | NULL        | Année de construction (`CONSTRUCT`) ; NULL si absente ou ≤ 1000. |
| `position`              | `geometry(Point)` (SRID 0) | NULL | Position (lon, lat) WGS84, convertie depuis `X`, `Y` dans la projection `EPSG` de la ligne (2154 Lambert-93, 2975 Réunion, 5490 Antilles, 2972 Guyane) ; SRID 0 comme `etablissement.position` (cohérent avec les isochrones). Index GiST `logement_social_position_gist`. NULL si non géolocalisé. |

**Index** : `logement_social_iris_idx (iris)` — sert aussi d'index pour la FK ; `logement_social_position_gist` (GiST sur `position`). Pas de clé primaire (la source n'a pas d'identifiant de logement).

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

## Table `fiscalite`

Taux de fiscalité locale des particuliers, par commune et exercice (DGFiP, « fiscalité locale des particuliers », fichier Parquet de data.gouv.fr). Créée par `internal/store/migrations/012_fiscalite.sql`, chargée par `scripts/import_fiscalite_parquet.py` (option `--start-year` pour ne garder que les exercices récents). Les lignes dont le code n'est pas dans `commune` sont ignorées.

| Colonne            | Type SQL           | Contraintes | Description |
|--------------------|--------------------|-------------|-------------|
| `code_commune`     | `TEXT`             | NOT NULL, **FK** → `commune.code_commune` | Code INSEE de la commune (colonne `INSEE COM`). |
| `year`             | `INTEGER`          | NOT NULL    | Exercice fiscal (colonne `EXERCICE`). |
| `taux_global_tfb`  | `DOUBLE PRECISION` | NULL        | Taux global de taxe foncière sur les propriétés bâties (%). |
| `taux_plein_teom`  | `DOUBLE PRECISION` | NULL        | Taux plein de taxe d'enlèvement des ordures ménagères (%) ; NULL si pas de TEOM. |
| `taux_global_tfnb` | `DOUBLE PRECISION` | NULL        | Taux global de taxe foncière sur les propriétés non bâties (%). |
| `taux_global_th`   | `DOUBLE PRECISION` | NULL        | Taux global de taxe d'habitation (%). |
| `optepci`          | `TEXT`             | NULL        | Régime fiscal de l'EPCI (`FPU`, `FPZ`, `FPA`) ; NULL pour une commune isolée. |

**Clé primaire** : `(code_commune, year)` — elle sert aussi d'index pour la FK.

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

`balance_cents` (`BIGINT`, NOT NULL, défaut `0`, `CHECK >= 0`, `migrations/016_billing.sql`)
est le solde de crédit prépayé, en centimes : somme des mouvements de `credit_transaction`,
tenue à jour dans la même transaction.

## Table `billing_settings`

Tarifs de facturation, ligne unique (`id = TRUE`), modifiables par un administrateur
(`migrations/016_billing.sql`). Montants en centimes.

| Colonne                  | Type SQL      | Description |
|--------------------------|---------------|-------------|
| `price_per_origin_cents` | `BIGINT`      | Prix par isochrone **calculée** lors d'un `POST /etablissements` (défaut 5) ; les isochrones lues dans `isochrone_cache` sont gratuites. Le nom de la colonne date de la recherche par points de départ. |
| `isochrone_price_cents`  | `BIGINT`      | Prix de `GET /isochrone` (défaut 2). |
| `welcome_credit_cents`   | `BIGINT`      | Crédit offert à la création d'un compte (défaut 100). |
| `min_topup_cents`        | `BIGINT`      | Rechargement minimum (défaut 500, au moins 100). |
| `currency`               | `TEXT`        | Devise (`EUR`). |
| `updated_at`, `updated_by` | `TIMESTAMPTZ`, `TEXT` | Dernière modification (FK `utilisateur`, ON DELETE SET NULL). |

## Table `credit_transaction`

Journal des mouvements de crédit (`migrations/016_billing.sql`).

| Colonne        | Type SQL      | Description |
|----------------|---------------|-------------|
| `id`           | `BIGSERIAL`   | **PK**. |
| `user_id`      | `TEXT`        | FK `utilisateur(id)` ON DELETE CASCADE. |
| `amount_cents` | `BIGINT`      | Montant signé (débit négatif). |
| `kind`         | `TEXT`        | `topup`, `usage`, `refund`, `admin_adjust` ou `welcome`. |
| `description`  | `TEXT`        | Libellé affiché (motif d'un ajustement). |
| `reference`    | `TEXT`        | Identifiant du paiement pour un `topup`. |
| `created_by`   | `TEXT`        | Administrateur auteur d'un ajustement. |
| `created_at`   | `TIMESTAMPTZ` | Date du mouvement. Index `(user_id, created_at DESC)`. |

## Table `payment`

Paiements de rechargement auprès de Mollie (`migrations/016_billing.sql`). Le passage
à `paid` et le crédit du compte ont lieu dans la même transaction, une seule fois.

| Colonne        | Type SQL      | Description |
|----------------|---------------|-------------|
| `id`           | `TEXT`        | **PK**, identifiant Mollie (`tr_…`). |
| `user_id`      | `TEXT`        | FK `utilisateur(id)` ON DELETE CASCADE. |
| `amount_cents`, `currency` | `BIGINT`, `TEXT` | Montant demandé. |
| `status`       | `TEXT`        | Statut Mollie (`open`, `pending`, `paid`, `failed`, `canceled`, `expired`…). |
| `mode`         | `TEXT`        | `test` ou `live`. |
| `created_at`, `paid_at` | `TIMESTAMPTZ` | Création et paiement. |

## Table `isochrone_cache`

Cache des isochrones OpenRouteService des recherches (`migrations/018_isochrone_cache.sql`), une par position, temps et mode de transport. Une isochrone créée il y a plus de `ISOCHRONE_CACHE_DAYS` jours (défaut 30) est ignorée puis recalculée ; à chaque écriture, les entrées expirées sont purgées et, au-delà de `ISOCHRONE_CACHE_MAX` lignes (défaut 20 000), les moins récemment utilisées sont supprimées.

| Colonne        | Type SQL      | Description |
|----------------|---------------|-------------|
| `lon_e5`, `lat_e5` | `INTEGER` | Position de l'établissement source, en 1e-5 degré (arrondie). |
| `dist`         | `INTEGER`     | Temps de trajet (secondes). |
| `transp`       | `TEXT`        | Profil OpenRouteService (`driving-car`, `cycling-regular`, `foot-walking`). |
| `ring`         | `JSONB`       | Anneau du polygone : tableau de `[lon, lat]`. |
| `created_at`   | `TIMESTAMPTZ` | Calcul de l'isochrone (index, pour la purge). |
| `last_used_at` | `TIMESTAMPTZ` | Dernière lecture (index, pour l'éviction LRU). |

**Clé primaire** : `(lon_e5, lat_e5, dist, transp)`.

## Table `email_verification`

Jetons de vérification d'adresse e-mail (`migrations/004_email_verification_logout.sql`).
Seul le hash du jeton est stocké ; un jeton est à usage unique et un nouvel envoi
remplace les jetons précédents de l'utilisateur.

| Colonne      | Type SQL      | Contraintes                         | Description |
|--------------|---------------|-------------------------------------|-------------|
| `token_hash` | `TEXT`        | **PK**                              | SHA-256 (hex) du jeton envoyé par e-mail. |
| `user_id`    | `TEXT`        | NOT NULL, FK `utilisateur(id)` ON DELETE CASCADE | Utilisateur à vérifier. |
| `expires_at` | `TIMESTAMPTZ` | NOT NULL                            | Expiration du lien (24 h). |
| `new_email`  | `TEXT`        | NULL                                | Nouvelle adresse pour un jeton de **changement d'e-mail** (`migrations/015_email_change.sql`) ; NULL pour un jeton d'inscription. L'adresse du compte n'est remplacée qu'à la vérification. |

## Table `oidc_identity`

Identités OpenID Connect rattachées à un compte (`migrations/014_oidc_identity.sql`).
Un compte peut avoir plusieurs identités ; un compte créé via OIDC n'a pas de mot de
passe (`utilisateur.password_hash` NULL).

| Colonne   | Type SQL | Contraintes | Description |
|-----------|----------|-------------|-------------|
| `issuer`  | `TEXT`   | **PK** (`issuer`, `subject`) | Émetteur OIDC (claim `iss`). |
| `subject` | `TEXT`   | **PK** (`issuer`, `subject`) | Identifiant chez le fournisseur (claim `sub`). |
| `user_id` | `TEXT`   | NOT NULL, FK `utilisateur(id)` ON DELETE CASCADE, indexé | Compte Douceville. |

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
- `Isochrone` — zone d'accessibilité (géométrie `POLYGON`) calculée par OpenRouteService ; les isochrones des recherches sont mises en cache dans `isochrone_cache`.
- `CommuneSecurite` — commune touchée par la zone : code, nom, `score` / `annee` / `nb_indicateurs` (dernière année de `securite_score`, `null` sans données) et `contour` GeoJSON ; renvoyée dans le champ `communes` de `POST /api/etablissements`.
- `LogementSocialPosition`, `LogementSocialGroupe` — logements sociaux de `logement_social` contenus dans la zone, regroupés par position (bâtiment) puis par caractéristiques identiques ; renvoyés dans le champ `logements_sociaux` de `POST /api/etablissements`.
- `ListeEtablissementsAvecIsochrone`, `SearchRequest` (`CritereNature`, `FiltresZone`), `Source`, `Centre` — réponse/requête de `POST /api/etablissements`.
- `QuartierProximite`, `Proche`, `ProximiteRequest` — fiche d'un quartier IRIS (`POST /api/quartiers/{iris}/proximite`).
- `IsochroneKey` — clé de `isochrone_cache`.

## Références dans le code

- Migrations SQL : `internal/store/migrations/*.sql` (runner : `internal/store/migrate.go`).
- Accès base : `internal/store/queries.go`, `internal/store/search.go`, `internal/store/billing.go` ; modèles : `internal/models/models.go`, `internal/models/billing.go`.
- Auth : `internal/auth/` (JWT `jwt.go`, mots de passe `password.go`, middleware `middleware.go`).
- Infrastructure PostGIS : `docker-compose.yml` (service `geotestdb`).
