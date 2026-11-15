# Douceville

Douceville aide à choisir où habiter en croisant une **adresse**, une **zone de
mobilité** (isochrone) et les **établissements scolaires** accessibles dans cette
zone, avec leurs résultats aux examens.

Donnez une adresse, une distance (ou un temps) de trajet et un mode de transport :
le service calcule la zone atteignable et renvoie la liste des établissements qui
s'y trouvent.

## Architecture

Le projet est composé de deux parties :

- **Backend HTTP (Go)** — l'API servie en production. Code dans `cmd/` et
  `internal/`. Authentification par token Bearer (JWT Supabase).
- **ETL (Python)** — outils d'import des données (parquet → PostgreSQL) et
  gestion des tokens de test, exposés par la CLI `dv-cli`. Code dans `src/douceville/`.

Les données sont stockées dans **PostgreSQL + PostGIS**. Deux services externes
sont utilisés :

- [OpenRouteService](https://openrouteservice.org/) pour le calcul des isochrones.
- [addok / Base Adresse Nationale](https://github.com/BaseAdresseNationale/addok-docker)
  pour le géocodage des adresses.

Le schéma de base de données est documenté dans [`schema.md`](schema.md).

## API

Toutes les routes sont montées sous `API_PATH` (vide par défaut) et protégées par
le middleware d'authentification Bearer.

| Méthode | Route                  | Description                                                        |
| ------- | ---------------------- | ------------------------------------------------------------------ |
| `GET`   | `/etablissement/{uai}` | Détail d'un établissement par son UAI.                              |
| `POST`  | `/etablissements`      | Établissements dans la zone atteignable depuis une adresse.        |
| `GET`   | `/isochrone`           | Isochrone depuis des coordonnées (`lat`, `lon`, `dist`, `transp`). |
| `GET`   | `/user`                | Profil de l'utilisateur authentifié.                               |

Le corps de `POST /etablissements` attend un JSON du type :

```json
{
  "adresse": "1 rue de la Paix, Paris",
  "dist": 15,
  "transp": "driving-car",
  "nature": null,
  "secteur": null
}
```

## Configuration

La configuration se fait par variables d'environnement. Copiez
[`sample.env`](sample.env) en `.env` et renseignez les valeurs.

Variables lues par le backend Go :

| Variable               | Défaut        | Rôle                                               |
| ---------------------- | ------------- | -------------------------------------------------- |
| `SUPABASE_JWT_SECRET`  | — (requis)    | Secret de validation des tokens Bearer.            |
| `POSTGRES_HOST`        | `localhost`   | Hôte PostgreSQL.                                   |
| `POSTGRES_PORT`        | `5432`        | Port PostgreSQL.                                   |
| `POSTGRES_DB`          | `douceville`  | Nom de la base.                                    |
| `POSTGRES_USER`        | `douceville`  | Utilisateur PostgreSQL.                            |
| `POSTGRES_PASSWORD`    | `douceville`  | Mot de passe PostgreSQL.                           |
| `ADDOK_HOST`           | — (vide)      | Hôte du service de géocodage addok.                |
| `OPENROUTESERVICE_KEY` | — (vide)      | Clé API OpenRouteService.                          |
| `API_PATH`             | — (vide)      | Préfixe de montage des routes (ex. `/api`).        |
| `LISTEN_ADDR`          | `:3566`       | Adresse d'écoute du serveur HTTP.                  |
| `LOGLEVEL`             | `INFO`        | Niveau de log.                                     |

Les autres variables Supabase de `sample.env` ne servent qu'à la CLI Python
(`dv-cli token …`).

## Développement

### Backend Go

Nécessite Go 1.26+.

```bash
go build ./cmd/server    # compiler
go run ./cmd/server      # lancer le serveur
go test ./...            # exécuter les tests
```

### ETL Python

Nécessite Python 3.11+ et [uv](https://docs.astral.sh/uv/).

```bash
uv sync                               # installer les dépendances
uv run dv-cli import etab <path>      # importer les établissements (parquet)
uv run dv-cli import resultats <path> # importer les résultats (parquet)
uv run dv-cli token create            # créer un token de test
uv run dv-cli token check <token>     # vérifier un token
```

## Déploiement

L'image est construite à partir du [`Dockerfile`](Dockerfile) multi-stage
(binaire statique Go sur base `distroless`).

```bash
docker build -t ydethe/douceville:latest .
```

Le déploiement de production utilise [`docker-compose.yml`](docker-compose.yml)
(backend + PostGIS, exposition via Traefik). Le service écoute sur le port `3566`.

## Licence

Voir [`LICENSE`](LICENSE).
