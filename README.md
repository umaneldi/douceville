# Douceville

Douceville aide à choisir où habiter en croisant une **adresse**, une **zone de
mobilité** (isochrone) et les **établissements scolaires** accessibles dans cette
zone, avec leurs résultats aux examens.

Donnez une adresse, un temps de trajet et un mode de transport : le service calcule
la zone atteignable et affiche sur une carte les établissements qui s'y trouvent.

## Sources de données

https://data.education.gouv.fr/explore/dataset/fr-en-adresse-et-geolocalisation-etablissements-premier-et-second-degre/information
https://data.education.gouv.fr/explore/assets/fr-en-indicateurs-de-resultat-des-lycees-gt_v2/
https://data.education.gouv.fr/explore/assets/fr-en-indicateurs-de-resultat-des-lycees-denseignement-general-et-technologique/
https://data.education.gouv.fr/explore/assets/fr-en-indicateurs-de-resultat-des-lycees-pro_v2/
https://data.education.gouv.fr/explore/assets/fr-en-indicateurs-de-resultat-des-lycees-denseignement-professionnels/
https://data.education.gouv.fr/explore/assets/fr-en-indicateurs-valeur-ajoutee-colleges/
https://www.data.gouv.fr/datasets/bases-statistiques-communale-departementale-et-regionale-de-la-delinquance-enregistree-par-la-police-et-la-gendarmerie-nationales?resource_id=604d71b8-337d-4869-9226-49e01bae87df
https://www.data.gouv.fr/datasets/code-officiel-geographique-1?resource_id=eb836ef4-8be2-4a55-bfdb-eb1ad041933e
https://etalab-datasets.geo.data.gouv.fr/contours-administratifs/2026/geojson/communes-50m.geojson.gz
https://www.data.gouv.fr/datasets/demandes-de-valeurs-foncieres-geolocalisees?resource_id=d7933994-2c66-4131-a4da-cf7cd18040a4
https://www.data.gouv.fr/datasets/fiscalite-locale-des-particuliers?resource_id=f48d0fcc-f732-445d-ba2d-886ec4952bce
https://www.data.gouv.fr/datasets/donnees-detaillees-au-logement-du-repertoire-des-logements-locatifs-des-bailleurs-sociaux-rpls
https://www.data.gouv.fr/datasets/revenus-et-pauvrete-des-menages-aux-niveaux-national-et-local-revenus-localises-sociaux-et-fiscaux

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
les autres exigent un header `Authorization: Bearer <token>` **et** un compte de
niveau `granted` ou `admin` (sinon `403`, `code=account_not_granted`).

| Méthode | Route                      | Auth | Description                                                   |
| ------- | -------------------------- | :--: | ------------------------------------------------------------- |
| `POST`  | `/api/auth/register`       |  —   | Inscription (email + mot de passe). Envoie un lien de vérification ; pas de token. |
| `POST`  | `/api/auth/verify`         |  —   | Valide l'e-mail avec le jeton du lien (`{token}`) : le compte passe à `verified`. Pas de token tant que le compte n'est pas `granted`. |
| `POST`  | `/api/auth/resend-verification` | — | Renvoie un lien de vérification (`{email}`). Réponse toujours `202`. |
| `POST`  | `/api/auth/login`          |  —   | Connexion, réservée aux comptes `granted`/`admin` (sinon `403`, `code=email_not_verified` ou `account_not_granted`). Renvoie `{token, user}`. |
| `POST`  | `/api/auth/logout`         |  ✓   | Déconnexion : révoque le token courant (`204`).               |
| `GET`   | `/api/user`                |  ✓   | Profil de l'utilisateur authentifié.                          |
| `PATCH` | `/api/user`                |  ✓   | Met à jour `{first_name, last_name, avatar_url}` (vide : effacé). |
| `POST`  | `/api/user/email`          |  ✓   | Demande de changement d'adresse (`{email, current_password}`) : lien de confirmation envoyé à la nouvelle adresse, qui ne remplace l'ancienne qu'après vérification (`202`). |
| `POST`  | `/api/user/password`       |  ✓   | Change le mot de passe (`{current_password, new_password}` ; mot de passe actuel non requis pour un compte OIDC sans mot de passe) (`204`). |
| `GET`   | `/api/admin/users`         | admin | Liste de tous les comptes (réservé au niveau `admin`, sinon `403`, `code=admin_required`). |
| `PATCH` | `/api/admin/users/{id}`    | admin | Change le niveau d'un compte (`{perms}`). Un compte qui obtient l'accès (`granted`/`admin`) est prévenu par e-mail. Impossible sur son propre compte (`422`). |
| `DELETE`| `/api/admin/users/{id}`    | admin | Supprime un compte (`204`). Impossible sur son propre compte (`422`). |
| `GET`   | `/api/auth/oidc/config`    |  —   | `{enabled, name}` : connexion OIDC disponible ?                |
| `GET`   | `/api/auth/oidc/login`     |  —   | Redirige vers le fournisseur OIDC.                            |
| `GET`   | `/api/auth/oidc/callback`  |  —   | Retour du fournisseur ; redirige vers `/#oidc_token=…` ou `/?oidc_error=…`. |
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
identifiant (UUID), hache le mot de passe (PBKDF2-SHA256) et envoie par e-mail un
lien de vérification `${PUBLIC_URL}/verify-email?token=…` (usage unique, valable
24 h).

Chaque compte a un niveau d'accès (`utilisateur.perms`, enum ordonné) :

| Niveau       | Signification                                        | Accès |
| ------------ | ---------------------------------------------------- | ----- |
| `unverified` | Compte créé, e-mail non vérifié (niveau initial).    | aucun |
| `verified`   | E-mail vérifié, en attente de validation.            | aucun |
| `granted`    | Compte validé par un administrateur.                 | application |
| `admin`      | Administrateur.                                      | application |

Seuls les comptes `granted` et `admin` reçoivent un token. Le niveau est relu en base
à chaque requête : rétrograder un compte lui retire l'accès immédiatement.

Les administrateurs disposent d'un panneau **Utilisateurs** (icône dans la barre
latérale) : liste des comptes (ceux en attente de validation en tête), changement
de niveau, suppression. Un compte validé reçoit un e-mail l'invitant à se
connecter. Un administrateur ne peut ni changer son propre niveau ni supprimer son
propre compte. Le premier administrateur se désigne avec
`scripts/set_user_perms.py set EMAIL admin`, ou en SQL :

```sql
UPDATE utilisateur SET perms = 'admin' WHERE lower(email) = lower('alice@example.com');
```

Les JWT HS256 sont signés avec `JWT_SECRET` et portent un identifiant unique
(`jti`). La déconnexion révoque ce `jti` côté serveur (table `revoked_token`)
jusqu'à l'expiration du token. Le frontend stocke le token et l'envoie en Bearer.

#### Connexion OpenID Connect (optionnelle)

Si `OIDC_ISSUER_URL` est défini, un bouton « Se connecter avec … » apparaît à côté
du formulaire (qui reste disponible). Le flux est un *authorization code* avec PKCE :
`/api/auth/oidc/login` redirige vers le fournisseur, qui revient sur
`/api/auth/oidc/callback` (URL à déclarer chez le fournisseur). Le backend vérifie
l'ID token puis émet **son propre JWT**, comme pour la connexion par mot de passe.

- Une identité inconnue crée un compte (sans mot de passe) au niveau `OIDC_DEFAULT_PERMS`.
- Si un compte existe déjà avec le même e-mail, l'identité lui est rattachée
  uniquement si le fournisseur atteste l'adresse (`email_verified`) ; sinon la
  connexion est refusée.
- Les identités sont stockées dans la table `oidc_identity` (issuer, subject).

Un utilisateur connecté modifie son compte en cliquant sur son avatar (affiché
depuis `avatar_url`, sinon son Gravatar, sinon ses initiales). Un changement
d'adresse e-mail est **en attente** jusqu'au clic sur le lien envoyé à la nouvelle
adresse (l'ancienne est prévenue) ; l'ancienne adresse reste utilisable jusque-là.
Un mauvais mot de passe actuel renvoie `403` (`code=invalid_password`).

Sans `SMTP_HOST`, les e-mails ne sont pas envoyés mais **journalisés** (niveau
`WARN`, lien inclus) : pratique en développement.

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
| `PUBLIC_URL`           | `http://localhost:3566` | URL publique, base des liens envoyés par e-mail. |
| `SMTP_HOST`            | — (vide)      | Serveur SMTP. Vide : e-mails journalisés, non envoyés. |
| `SMTP_PORT`            | `587`         | Port SMTP (`465` : TLS implicite ; sinon STARTTLS si proposé). |
| `SMTP_USERNAME`        | — (vide)      | Identifiant SMTP (pas d'authentification si vide). |
| `SMTP_PASSWORD`        | — (vide)      | Mot de passe SMTP.                                 |
| `SMTP_FROM`            | — (requis si `SMTP_HOST`) | Expéditeur, ex. `Douceville <no-reply@domaine.tld>`. |
| `OIDC_ISSUER_URL`      | — (vide)      | URL de l'émetteur OIDC, à l'identique du champ `issuer` de `.well-known/openid-configuration` (slash final compris). Vide : connexion OIDC désactivée. |
| `OIDC_CLIENT_ID`       | — (requis si OIDC) | Identifiant du client OIDC.                 |
| `OIDC_CLIENT_SECRET`   | — (vide)      | Secret du client (vide pour un client public, PKCE). |
| `OIDC_PROVIDER_NAME`   | `SSO`         | Libellé du bouton « Se connecter avec … ».        |
| `OIDC_SCOPES`          | `openid email profile` | Scopes demandés (séparés par des espaces). |
| `OIDC_REDIRECT_URL`    | `${PUBLIC_URL}/api/auth/oidc/callback` | URL de retour déclarée chez le fournisseur. |
| `OIDC_DEFAULT_PERMS`   | `verified`    | Niveau des comptes créés via OIDC (`granted` : accès immédiat). |

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
   (≥ 8 caractères), puis ouvrez le lien de vérification reçu par e-mail (sans
   SMTP configuré, le lien apparaît dans les logs du serveur).
3. Validez le compte (`perms = 'granted'`, voir [Authentification](#authentification)),
   puis connectez-vous.
4. Saisissez une adresse, un temps de trajet et un mode de transport, puis
   **Rechercher**.

> **À noter.** La recherche d'établissements nécessite, en plus, un
> `OPENROUTESERVICE_KEY` (isochrones), un `ADDOK_HOST` (géocodage) et des données
> chargées dans les tables `etablissement`/`resultat` (il n'y a pas d'ETL dans ce
> dépôt — voir plus haut). L'inscription, la connexion et l'interface fonctionnent
> sans ces éléments.
>
> Le référentiel des communes (table `commune`) se charge depuis le fichier INSEE
> `v_commune_depuis_1943.csv` :
> `uv run scripts/import_communes_csv.py v_commune_depuis_1943.csv [--dsn URL | --psql CMD]`.
> Chargez-le **avant** les établissements : `etablissement.code_commune` y fait
> référence (les codes absents sont enregistrés à NULL).
>
> Les données de délinquance par commune (table `securite`) se chargent ensuite
> depuis le Parquet communal du SSMSI (data.gouv.fr) :
> `uv run scripts/import_securite_parquet.py donnee-comm-data.gouv-parquet-….parquet [--dsn URL | --psql CMD]`.
>
> Les contours des communes (colonne `commune.contour`, pour la carte de sécurité)
> se chargent depuis les contours administratifs Etalab (même millésime COG) :
> `uv run scripts/import_contours_communes.py communes-50m.geojson.gz [--dsn URL | --psql CMD]`.
> Le score de sécurité (vue matérialisée `securite_score`) est recalculé à la fin
> de l'import `securite`.
> `import.sh` enchaîne tous les imports dans le bon ordre.

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
