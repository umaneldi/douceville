# Douceville

Douceville aide à choisir où habiter en croisant une **adresse**, une **zone de
mobilité** (isochrone) et les **établissements scolaires** accessibles dans cette
zone, avec leurs résultats aux examens.

Donnez une adresse, un temps de trajet et un mode de transport : le service calcule
la zone atteignable et affiche sur une carte les établissements qui s'y trouvent.

## Sources de données

https://data.education.gouv.fr/explore/dataset/fr-en-adresse-et-geolocalisation-etablissements-premier-et-second-degre/information
https://data.education.gouv.fr/explore/assets/fr-en-indicateurs-de-resultat-des-lycees-gt_v2/
https://data.education.gouv.fr/explore/assets/fr-en-indicateurs-valeur-ajoutee-colleges/

## Architecture

Une **image Docker unique** sert à la fois l'API et l'interface web :

- **Backend (Go)** — API JSON sous `/api`, authentification native par JWT
  (Bearer). Code dans `cmd/` et `internal/`.
- **Frontend (HTML/JS/CSS)** — interface cartographique (Leaflet) servie à la
  racine `/`, embarquée dans le binaire via `//go:embed`. Code dans `web/`.

Les données sont stockées dans **PostgreSQL + PostGIS**. Deux services externes
sont utilisés :

- [OpenRouteService](https://openrouteservice.org/) pour le calcul des isochrones.
- [addok / Base Adresse Nationale](https://github.com/BaseAdresseNationale/addok-docker)
  pour le géocodage des adresses.

Le schéma de base de données est documenté dans [`schema.md`](schema.md). Les tables
sont créées au démarrage par un petit runner de migrations
(`internal/store/migrate.go`, SQL embarqué dans `internal/store/migrations/`).

> **Données.** Ce dépôt ne contient pas d'ETL : les tables `etablissement` et
> `resultat` sont créées vides, les données sont chargées par un moyen externe
> (dump PostgreSQL restauré, chargeur dédié…).

## API

Toutes les routes sont sous `/api`. Les routes d'authentification sont publiques ;
les autres exigent un header `Authorization: Bearer <token>`.

| Méthode | Route                      | Auth | Description                                                   |
| ------- | -------------------------- | :--: | ------------------------------------------------------------- |
| `POST`  | `/api/auth/register`       |  —   | Inscription (email + mot de passe). Renvoie `{token, user}`.  |
| `POST`  | `/api/auth/login`          |  —   | Connexion. Renvoie `{token, user}`.                           |
| `GET`   | `/api/user`                |  ✓   | Profil de l'utilisateur authentifié.                          |
| `GET`   | `/api/etablissement/{uai}` |  ✓   | Détail d'un établissement par son UAI.                        |
| `POST`  | `/api/etablissements`      |  ✓   | Établissements dans la zone atteignable depuis une adresse.   |
| `GET`   | `/api/isochrone`           |  ✓   | Isochrone depuis des coordonnées (`lat`, `lon`, `dist`, `transp`). |

Exemple de corps pour `POST /api/etablissements` (`dist` est un **temps en secondes**) :

```json
{
  "adresse": "1 rue de la Paix, Paris",
  "dist": 900,
  "transp": "driving-car",
  "nature": ["Lycée", "Collège"],
  "secteur": ["public"]
}
```

### Authentification

L'auth est **native** (plus de Supabase). À l'inscription, le backend génère un
identifiant (UUID), hache le mot de passe (PBKDF2-SHA256) et émet un JWT HS256
signé avec `JWT_SECRET`. Le frontend stocke le token et l'envoie en Bearer.

## Configuration

Copiez [`sample.env`](sample.env) en `.env` et renseignez les valeurs. Variables
lues par le backend :

| Variable               | Défaut        | Rôle                                               |
| ---------------------- | ------------- | -------------------------------------------------- |
| `JWT_SECRET`           | — (requis)    | Secret de signature/validation des JWT. `openssl rand -hex 32`. |
| `POSTGRES_HOST`        | `localhost`   | Hôte PostgreSQL.                                   |
| `POSTGRES_PORT`        | `5432`        | Port PostgreSQL.                                   |
| `POSTGRES_DB`          | `douceville`  | Nom de la base.                                    |
| `POSTGRES_USER`        | `douceville`  | Utilisateur PostgreSQL.                            |
| `POSTGRES_PASSWORD`    | `douceville`  | Mot de passe PostgreSQL.                           |
| `ADDOK_HOST`           | — (vide)      | Hôte du service de géocodage addok.                |
| `OPENROUTESERVICE_KEY` | — (vide)      | Clé API OpenRouteService.                          |
| `LISTEN_ADDR`          | `:3566`       | Adresse d'écoute du serveur HTTP.                  |
| `LOGLEVEL`             | `INFO`        | Niveau de log.                                     |

## Lancer le service

Le service a besoin de **deux choses** : une base **PostgreSQL/PostGIS** et un
**`JWT_SECRET`**. Au démarrage, il applique ses migrations (création des tables)
puis sert l'interface sur `http://localhost:3566` et l'API sur `/api`.

### Option A — Docker (recommandé)

```bash
# 1. Une base PostGIS (ou utilisez la vôtre)
docker run -d --name douceville-db \
  -e POSTGRES_DB=douceville -e POSTGRES_USER=douceville -e POSTGRES_PASSWORD=douceville \
  -p 5432:5432 postgis/postgis

# 2. L'application (image monolithique : UI + API)
docker build -t douceville .
docker run --rm -p 3566:3566 \
  -e JWT_SECRET="$(openssl rand -hex 32)" \
  -e POSTGRES_HOST=host.docker.internal \
  -e POSTGRES_PASSWORD=douceville \
  douceville
```

### Option B — Depuis les sources (Go 1.26+)

```bash
# 1. Lancer PostGIS (compose de dev : service « db »)
./restart.sh

# 2. Lancer le serveur
JWT_SECRET=dev-secret POSTGRES_HOST=localhost go run ./cmd/server
```

### Première utilisation

1. Ouvrez `http://localhost:3566`.
2. Cliquez sur **« Créer un compte »**, saisissez un e-mail et un mot de passe
   (≥ 8 caractères) : l'inscription est ouverte et vous connecte directement.
3. Saisissez une adresse, un temps de trajet et un mode de transport, puis
   **Rechercher**.

> **À noter.** La recherche d'établissements nécessite, en plus, un
> `OPENROUTESERVICE_KEY` (isochrones), un `ADDOK_HOST` (géocodage) et des données
> chargées dans les tables `etablissement`/`resultat` (il n'y a pas d'ETL dans ce
> dépôt — voir plus haut). L'inscription, la connexion et l'interface fonctionnent
> sans ces éléments.

## Développement

Nécessite Go 1.26+ et une base PostGIS.

```bash
# 1. Lancer PostGIS (compose de dev)
./restart.sh

# 2. Lancer le serveur (sert l'UI sur http://localhost:3566 et l'API sur /api)
JWT_SECRET=dev-secret go run ./cmd/server

# Qualité
go build ./...
go vet ./...
go test ./...
```

Le frontend est en HTML/CSS/JS statique (sans étape de build) dans `web/` ; il est
embarqué dans le binaire à la compilation. Modifier un fichier de `web/` nécessite
donc une recompilation (`go run` / `go build`).

## Déploiement

L'image est construite à partir du [`Dockerfile`](Dockerfile) multi-stage (binaire
statique Go sur base `distroless`), qui embarque l'UI et les migrations.

```bash
docker build -t douceville .
docker run -p 3566:3566 \
  -e JWT_SECRET=... -e POSTGRES_HOST=... -e POSTGRES_PASSWORD=... \
  douceville
```

Le déploiement de production utilise [`docker-compose.yml`](docker-compose.yml)
(application + PostGIS, exposition via Traefik). Le conteneur écoute sur `3566` et
sert l'UI **et** l'API.

## Licence

Voir [`LICENSE`](LICENSE).
