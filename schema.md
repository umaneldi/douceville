# Schéma de la base de données

Ce document décrit le schéma de la base de données du projet **Douceville**.

## Vue d'ensemble

- **SGBD** : PostgreSQL avec l'extension **PostGIS** (image Docker `postgis/postgis`).
- **ORM** : [SQLModel](https://sqlmodel.tiangolo.com/) (couche au-dessus de SQLAlchemy) combiné à [GeoAlchemy2](https://geoalchemy-2.readthedocs.io/) pour les types géographiques.
- **Création du schéma** : les tables sont créées automatiquement via `SQLModel.metadata.create_all(engine)` (voir `get_engine()` et `create_db_and_tables()` dans `src/douceville/schemas.py`). Il n'existe pas de fichier de migration SQL.
- **Chaîne de connexion** : `postgresql+psycopg2://<user>:<password>@<host>/<db>` construite dans `src/douceville/config.py` à partir des variables d'environnement `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_HOST`, `POSTGRES_DB`.

La base contient **trois tables physiques** : `etablissement`, `resultat` et `utilisateur`. Les deux premières sont gérées par le backend Python (SQLModel) ; la table `utilisateur` est introduite par le backend Go (voir plus bas). Les autres classes du module `schemas.py` sont des modèles Pydantic/SQLModel non persistés (DTO d'entrée/sortie de l'API, objets métier comme `Isochrone`).

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
│ position   (POINT)      │          └──────────────────────────┘
│ departement             │          UNIQUE (diplome, annee,
│ academie                │                  etablissement_uai)
│ secteur                 │
│ ouverture               │
│ nature                  │
└─────────────────────────┘

Relation : etablissement (1) ──< (N) resultat
  - clé étrangère : resultat.etablissement_uai → etablissement.UAI
  - ON DELETE CASCADE (la suppression d'un établissement supprime ses résultats)
```

## Table `etablissement`

Établissement scolaire (lycée, collège, etc.). Définie par la classe `Etablissement` (`EtablissementBase` + `table=True`).

| Colonne       | Type SQL              | Contraintes                     | Description |
|---------------|-----------------------|---------------------------------|-------------|
| `id`          | `INTEGER`             | **PK**, auto-incrément          | Identifiant interne. |
| `UAI`         | `VARCHAR`             | NOT NULL, **UNIQUE**            | Code UAI (Unité Administrative Immatriculée), identifiant national de l'établissement ; sert de cible de la clé étrangère depuis `resultat`. |
| `nom`         | `VARCHAR`             | NOT NULL                        | Nom de l'établissement. |
| `adresse`     | `VARCHAR`             | NULL                            | Adresse (voie). |
| `lieu_dit`    | `VARCHAR`             | NULL                            | Lieu-dit. |
| `code_postal` | `VARCHAR`             | NOT NULL                        | Code postal. |
| `commune`     | `VARCHAR`             | NOT NULL                        | Commune. |
| `position`    | `GEOMETRY(POINT)`     | NOT NULL                        | Position géographique (PostGIS). Exposée en GeoJSON côté API via `ST_AsGeoJSON`. |
| `departement` | `VARCHAR`             | NOT NULL                        | Code / nom du département. |
| `academie`    | `VARCHAR`             | NULL                            | Académie de rattachement. |
| `secteur`     | `VARCHAR`             | NOT NULL                        | Secteur (`public` / `privé`). |
| `ouverture`   | `TIMESTAMP`           | NULL                            | Date d'ouverture. |
| `nature`      | `VARCHAR`             | NOT NULL                        | Nature de l'établissement (ex. `lycée`, `collège`). |

**Relations** : `resultats` → liste des `Resultat` liés (`back_populates="etablissement"`, suppression en cascade).

### Détails sur `position`
La colonne est déclarée via GeoAlchemy2 : `Column(Geometry("POINT"))`. En lecture, un validateur Pydantic (`convert_geom_to_geojson`) convertit le `WKBElement` PostGIS en GeoJSON. En écriture, la valeur peut être fournie en WKT (ex. `"POINT(1.4401531 43.6085909)"`).

## Table `resultat`

Résultat d'un examen pour un établissement et une année donnés. Définie par la classe `Resultat` (`ResultatBase` + `table=True`).

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

**Relations** : `etablissement` → l'`Etablissement` parent (`back_populates="resultats"`).

## Table `utilisateur`

Profil des utilisateurs authentifiés, introduite par le **backend Go**. Après
validation du JWT (HS256, secret Supabase, audience `authenticated`), le backend
récupère le profil dans cette table au lieu d'appeler l'API REST Supabase. La
table est créée de façon idempotente au démarrage (`CREATE TABLE IF NOT EXISTS`,
voir `migrations/001_utilisateur.sql` et `internal/store/queries.go`).

| Colonne       | Type SQL   | Contraintes      | Description |
|---------------|------------|------------------|-------------|
| `id`          | `TEXT`     | **PK**           | Identifiant de l'utilisateur ; correspond au claim `sub` du JWT (UUID Supabase). |
| `email`       | `TEXT`     | NULL             | Adresse e-mail. |
| `first_name`  | `TEXT`     | NULL             | Prénom. |
| `last_name`   | `TEXT`     | NULL             | Nom. |
| `avatar_url`  | `TEXT`     | NULL             | URL de l'avatar. |
| `permissions` | `TEXT[]`   | NOT NULL, défaut `'{}'` | Liste des permissions. |

## Modèles non persistés (hors base)

Ces classes n'ont pas `table=True` et ne correspondent à aucune table ; elles servent aux échanges API ou à la logique métier :

- `DvUser` — représentation d'un utilisateur authentifié (auth gérée par **Supabase**, hors de cette base).
- `EtablissementCreate`, `EtablissementPublic`, `EtablissementUpdate`, `EtablissementPublicAvecResultats` — DTO pour la table `etablissement`.
- `ResultatCreate`, `ResultatPublic`, `ResultatUpdate`, `ResultatPublicAvecEtablissement` — DTO pour la table `resultat`.
- `Isochrone` — zone d'accessibilité (géométrie `POLYGON`) calculée à la volée, non stockée.
- `ListeEtablissementPublicAvecResultatsEtIsochrone`, `QueryParameters` — structures de requête/réponse de l'API.

## Références dans le code

- Définition des tables (Python) : `src/douceville/schemas.py`
- Connexion et initialisation (Python) : `src/douceville/config.py`
- Backend Go : modèles `internal/models/models.go`, accès DB `internal/store/queries.go`, migration `migrations/001_utilisateur.sql`
- Infrastructure PostGIS : `docker-compose.yml`, `docker-compose.dev.yml`
