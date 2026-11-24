# Douceville

Douceville aide à choisir où habiter. À partir d'un ou plusieurs **points de
départ** (adresse + temps de trajet + mode de transport), le service calcule la
**zone atteignable** depuis tous ces points (intersection des isochrones) et
affiche sur une carte ce qu'elle contient :

- les **établissements scolaires** et leurs résultats aux examens (bac, brevet) ;
- la **sécurité** des communes (score de délinquance SSMSI) et leur **fiscalité
  locale** (taxe foncière, TEOM) ;
- le **prix de l'immobilier** au m² (ventes DVF) par commune et par quartier IRIS ;
- les **logements sociaux** (répertoire RPLS) ;
- des **niveaux de temps de trajet** emboîtés (≤ 5 min, ≤ 10 min…) pour situer
  chaque résultat dans la zone.

L'état de la page (points de départ, filtres, couches, onglet) est reflété dans
l'URL : un favori ou un lien partagé restaure la recherche.

## Sommaire

- [Architecture](#architecture)
- [Organisation du dépôt](#organisation-du-dépôt)
- [Démarrage rapide](#démarrage-rapide)
- [Chargement des données](#chargement-des-données)
- [Configuration](#configuration)
- [API](#api)
- [Comptes et authentification](#comptes-et-authentification)
- [Facturation (crédit prépayé, Mollie)](#facturation-crédit-prépayé-mollie)
- [Développement](#développement)
- [Déploiement](#déploiement)
- [Sources de données](#sources-de-données)

## Architecture

Une **image Docker unique** (un seul binaire Go) sert à la fois l'API et
l'interface web :

```
navigateur ──► / (UI statique embarquée, web/)
           └─► /api (API JSON, internal/api)
                    │
                    ├── PostgreSQL + PostGIS (internal/store)
                    ├── OpenRouteService : isochrones (internal/geo)
                    ├── addok / Base Adresse Nationale : géocodage (internal/geo)
                    ├── SMTP : e-mails transactionnels (internal/mail)
                    ├── fournisseur OpenID Connect, optionnel (internal/auth)
                    └── Mollie : paiements, optionnel (internal/billing)
```

- **Backend (Go 1.26+)** — routeur [chi](https://github.com/go-chi/chi), accès
  base avec [pgx](https://github.com/jackc/pgx) (sans ORM), authentification
  native par JWT (Bearer), journaux structurés `log/slog`.
- **Frontend** — HTML/CSS/JS statique sans étape de build (carte
  [Leaflet](https://leafletjs.com/)), embarqué dans le binaire via `//go:embed`.
- **Base de données** — PostgreSQL + PostGIS. Le schéma est créé au démarrage
  par un runner de migrations SQL idempotentes (voir
  [`internal/store`](internal/store/README.md)) et documenté dans
  [`schema.md`](schema.md).

### Déroulement d'une recherche (`POST /api/etablissements`)

1. Le prix de la recherche est débité du crédit de l'utilisateur (si la
   facturation est active ; remboursé en cas d'échec).
2. Chaque adresse de départ est géocodée (BAN) puis son isochrone est calculée
   (OpenRouteService), en parallèle, avec des niveaux emboîtés tous les 5 min
   (10 min au-delà de 50 min).
3. PostGIS calcule l'intersection des isochrones : la **zone**, et ses niveaux
   intérieurs.
4. Les établissements, communes (sécurité, fiscalité), prix immobiliers et
   logements sociaux de la zone sont lus en base ; chaque établissement et
   logement social est étiqueté avec le plus petit niveau de temps qui le
   contient.

## Organisation du dépôt

| Chemin | Rôle |
| ------ | ---- |
| [`cmd/server/`](cmd/server/main.go) | Point d'entrée : charge la configuration, se connecte à la base, applique les migrations, assemble les dépendances et lance le serveur HTTP. |
| [`internal/api/`](internal/api/README.md) | Routeur HTTP, handlers de l'API et service de l'UI embarquée. |
| [`internal/auth/`](internal/auth/README.md) | JWT, middleware d'authentification, mots de passe, jetons de vérification, OpenID Connect. |
| [`internal/billing/`](internal/billing/README.md) | Prestataires de paiement (Mollie) pour le crédit prépayé. |
| [`internal/config/`](internal/config/README.md) | Lecture et validation des variables d'environnement. |
| [`internal/geo/`](internal/geo/README.md) | Clients OpenRouteService (isochrones) et addok/BAN (géocodage, autocomplétion). |
| [`internal/mail/`](internal/mail/README.md) | Envoi des e-mails (SMTP, ou simple journalisation en développement). |
| [`internal/models/`](internal/models/README.md) | Structures de données échangées par l'API et entre paquets. |
| [`internal/store/`](internal/store/README.md) | Accès PostgreSQL/PostGIS et migrations SQL embarquées. |
| [`web/`](web/embed.go) | Interface web (`index.html`, `app.js`, `app.css`), embarquée dans le binaire. |
| [`scripts/`](scripts/) | Scripts Python (exécutés avec [`uv`](https://docs.astral.sh/uv/)) : import des données, gestion des comptes, rôle en lecture seule. |
| [`schema.md`](schema.md) | Documentation du schéma de base de données. |
| [`CLAUDE.md`](CLAUDE.md) | Consignes pour les agents de code (et règles de maintenance de la documentation). |
| `Dockerfile`, `docker-compose.yml` | Image de production et pile Docker (PostGIS + application). |
| `launch_server.sh`, `restart.sh`, `import.sh` | Lancement local du serveur, de la base, et import complet des données. |

## Démarrage rapide

Le service a besoin de **deux choses** : une base **PostgreSQL/PostGIS** et un
**`JWT_SECRET`**. Au démarrage, il applique ses migrations (création des tables)
puis sert l'interface sur `http://localhost:3566` et l'API sur `/api`.

### Option A — Docker

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
cp sample.env .env          # puis renseigner les valeurs
./restart.sh                # (re)lance PostGIS via docker compose (service « geotestdb »)
./launch_server.sh          # charge .env et lance `go run ./cmd/server`
```

`launch_server.sh` exige `JWT_SECRET`, `OPENROUTESERVICE_KEY` et
`POSTGRES_PASSWORD` dans `.env`. Sans le script :

```bash
JWT_SECRET=dev-secret POSTGRES_HOST=127.0.0.1 go run ./cmd/server
```

### Première utilisation

1. Ouvrez `http://localhost:3566`.
2. Cliquez sur **« Créer un compte »**, saisissez un e-mail et un mot de passe
   (≥ 8 caractères), puis ouvrez le lien de vérification reçu par e-mail. Sans
   SMTP configuré, le lien apparaît dans les journaux du serveur (niveau `WARN`).
   La vérification donne directement accès à l'application.
3. Promouvez votre compte administrateur si besoin :
   `uv run scripts/set_user_perms.py set EMAIL admin`.
4. Saisissez une adresse, un temps de trajet et un mode de transport, puis
   **Rechercher**.

> La recherche nécessite un `OPENROUTESERVICE_KEY` (isochrones) et des données
> chargées en base (voir ci-dessous). Le géocodage utilise l'API publique de la
> BAN si `ADDOK_HOST` est vide. L'inscription, la connexion et l'interface
> fonctionnent sans ces éléments.

## Chargement des données

Les tables de données sont créées **vides** par les migrations et remplies par
les scripts de `scripts/` (Python, dépendances déclarées en tête de chaque
script, à lancer avec `uv run`). Chaque script accepte `--dsn URL` (connexion
directe) ou `--psql CMD` (ex. `--psql "docker exec -i <conteneur> psql -U
douceville -d douceville"`), et la plupart `--dry-run`.

| Ordre | Script | Table(s) alimentée(s) | Source |
| :---: | ------ | --------------------- | ------ |
| 1 | `import_communes_csv.py` | `commune` | INSEE, `v_commune_depuis_1943.csv` |
| 2 | `import_contours_communes.py` | `commune.contour` | Etalab, contours administratifs `communes-50m.geojson.gz` |
| 3 | `import_contours_iris.py` | `quartier` | IGN, contours IRIS (WFS, ou fichier sauvegardé avec `--save`) |
| 4 | `import_immobilier_dvf.py` | `immobilier` | DVF géolocalisées |
| 5 | `import_logement_social.py` | `logement_social` | RPLS |
| 6 | `import_etablissements_parquet.py` | `etablissement` | Annuaire géolocalisé des établissements |
| 7 | `import_resultats_parquet.py` | `resultat` | Indicateurs de résultats lycées / collèges |
| 8 | `import_securite_parquet.py` | `securite` (+ rafraîchit `securite_score`) | SSMSI, délinquance communale |
| 9 | `import_fiscalite_parquet.py` | `fiscalite` | DGFiP, fiscalité locale des particuliers |

Les communes doivent être chargées **en premier** (les autres tables y font
référence par `code_commune`), et les IRIS avant DVF et RPLS. [`import.sh`](import.sh)
enchaîne tous les imports dans le bon ordre à partir des fichiers placés dans
`.parquet/`.

Autres scripts :

- `set_user_perms.py` — gestion des comptes en ligne de commande (`list`,
  `show`, `set EMAIL LEVEL`, `add`, `remove`).
- `create_readonly_user.py` — crée un rôle PostgreSQL en lecture seule. Un
  serveur connecté avec un tel rôle **saute les migrations** au démarrage (le
  schéma doit déjà être à jour).

## Configuration

Copiez [`sample.env`](sample.env) en `.env` et renseignez les valeurs. Variables
lues par le backend ([`internal/config`](internal/config/README.md)) :

| Variable               | Défaut        | Rôle                                               |
| ---------------------- | ------------- | -------------------------------------------------- |
| `JWT_SECRET`           | — (requis)    | Secret de signature des JWT (et du cookie de flux OIDC). `openssl rand -hex 32`. |
| `POSTGRES_HOST`        | `localhost`   | Hôte PostgreSQL.                                   |
| `POSTGRES_PORT`        | `5432`        | Port PostgreSQL.                                   |
| `POSTGRES_DB`          | `douceville`  | Nom de la base.                                    |
| `POSTGRES_USER`        | `douceville`  | Utilisateur PostgreSQL.                            |
| `POSTGRES_PASSWORD`    | `douceville`  | Mot de passe PostgreSQL.                           |
| `ADDOK_HOST`           | — (vide)      | Hôte d'une instance addok (`http://<hôte>:7878`). Vide : API publique BAN (`data.geopf.fr`). |
| `OPENROUTESERVICE_KEY` | — (vide)      | Clé API OpenRouteService.                          |
| `LISTEN_ADDR`          | `:3566`       | Adresse d'écoute du serveur HTTP.                  |
| `LOGLEVEL`             | `INFO`        | `DEBUG`, `INFO`, `WARN`, `ERROR`.                  |
| `PUBLIC_URL`           | `http://localhost:3566` | URL publique : base des liens envoyés par e-mail et des URL de retour (OIDC, Mollie). |
| `SMTP_HOST`            | — (vide)      | Serveur SMTP. Vide : e-mails journalisés, non envoyés. |
| `SMTP_PORT`            | `587`         | Port SMTP (`465` : TLS implicite ; sinon STARTTLS si proposé). |
| `SMTP_USERNAME`        | — (vide)      | Identifiant SMTP (pas d'authentification si vide). |
| `SMTP_PASSWORD`        | — (vide)      | Mot de passe SMTP.                                 |
| `SMTP_FROM`            | — (requis si `SMTP_HOST`) | Expéditeur, ex. `Douceville <no-reply@domaine.tld>`. |
| `OIDC_ISSUER_URL`      | — (vide)      | URL de l'émetteur OIDC, à l'identique du champ `issuer` de `.well-known/openid-configuration` (slash final compris). Vide : OIDC désactivé. |
| `OIDC_CLIENT_ID`       | — (requis si OIDC) | Identifiant du client OIDC.                 |
| `OIDC_CLIENT_SECRET`   | — (vide)      | Secret du client (vide pour un client public, PKCE). |
| `OIDC_PROVIDER_NAME`   | `SSO`         | Libellé du bouton « Se connecter avec … ».        |
| `OIDC_SCOPES`          | `openid email profile` | Scopes demandés (`openid` est toujours ajouté). |
| `OIDC_REDIRECT_URL`    | `${PUBLIC_URL}/api/auth/oidc/callback` | URL de retour déclarée chez le fournisseur. |
| `OIDC_DEFAULT_PERMS`   | `verified`    | Niveau des comptes créés via OIDC (`granted` : accès immédiat). |
| `BILLING_ENABLED`      | `false`       | Active la facturation prépayée (`true`/`1`/`yes`/`on`). Sinon tout est gratuit. |
| `MOLLIE_API_KEY`       | — (requis si facturation) | Clé Mollie : `test_…` (paiements simulés) ou `live_…`. |
| `MOLLIE_WEBHOOK_URL`   | `${PUBLIC_URL}/api/billing/webhook/mollie` | URL notifiée par Mollie (non transmise si locale). |

`sample.env` contient aussi des variables propres au déploiement
(`TRAEFIK_NETWORK`, `HOST`, `HOME_*`…), lues par `docker-compose.yml` et non par
le backend.

## API

Toutes les routes sont sous `/api` ; les erreurs sont renvoyées sous la forme
`{"detail": "...", "code": "..."}` (`code` facultatif). Les routes marquées ✓
exigent un header `Authorization: Bearer <token>` **et** un compte de niveau
`granted` ou `admin` (sinon `403`, `code=account_not_granted`) ; les routes
*admin* exigent en plus le niveau `admin` (sinon `403`, `code=admin_required`).

### Authentification

| Méthode | Route | Auth | Description |
| ------- | ----- | :--: | ----------- |
| `POST` | `/api/auth/register` | — | Inscription (`{email, password, first_name?, last_name?}`). Envoie un lien de vérification ; aucun token. |
| `POST` | `/api/auth/verify/info` | — | Compte visé par un lien de vérification (`{token}`), sans consommer le jeton. |
| `POST` | `/api/auth/verify` | — | Consomme le jeton (`{token}`) : inscription → le compte passe à `granted` et reçoit `{token, user}` ; changement d'e-mail → la nouvelle adresse remplace l'ancienne. |
| `POST` | `/api/auth/resend-verification` | — | Renvoie un lien de vérification (`{email}`). Réponse toujours `202`. |
| `POST` | `/api/auth/login` | — | Connexion (`{email, password}`), réservée aux comptes `granted`/`admin` (sinon `403`, `code=email_not_verified` ou `account_not_granted`). Renvoie `{token, user}`. |
| `POST` | `/api/auth/logout` | ✓ | Révoque le token courant (`204`). |
| `GET` | `/api/auth/oidc/config` | — | `{enabled, name}` : connexion OIDC disponible ? |
| `GET` | `/api/auth/oidc/login` | — | Redirige vers le fournisseur OIDC. |
| `GET` | `/api/auth/oidc/callback` | — | Retour du fournisseur ; redirige vers `/#oidc_token=…` ou `/?oidc_error=…`. |

### Compte

| Méthode | Route | Auth | Description |
| ------- | ----- | :--: | ----------- |
| `GET` | `/api/user` | ✓ | Profil de l'utilisateur authentifié (dont `perms`, `has_password`, `pending_email`, `balance_cents`). |
| `PATCH` | `/api/user` | ✓ | Met à jour `{first_name, last_name, avatar_url}` (vide : effacé). |
| `POST` | `/api/user/email` | ✓ | Demande de changement d'adresse (`{email, current_password}`) : lien envoyé à la nouvelle adresse, l'ancienne est prévenue (`202`). |
| `POST` | `/api/user/password` | ✓ | Change le mot de passe (`{current_password, new_password}` ; mot de passe actuel non requis pour un compte OIDC sans mot de passe) (`204`). Mauvais mot de passe : `403`, `code=invalid_password`. |

### Recherche

| Méthode | Route | Auth | Description |
| ------- | ----- | :--: | ----------- |
| `POST` | `/api/etablissements` | ✓ | Recherche dans la zone atteignable (payante). |
| `GET` | `/api/etablissement/{uai}` | ✓ | Détail d'un établissement et de ses résultats. |
| `GET` | `/api/isochrone` | ✓ | Isochrone depuis des coordonnées (`lat`, `lon`, `dist`, `transp`) (payante). |
| `GET` | `/api/address/suggest?q=…` | ✓ | Autocomplétion d'adresse (5 suggestions, à partir de 3 caractères ; gratuite). |

Corps de `POST /api/etablissements` (`dist` est un **temps en secondes** ;
`transp` vaut `driving-car`, `cycling-regular` ou `foot-walking` ; au plus 10
points de départ) :

```json
{
  "origines": [
    {"adresse": "1 rue de la Paix, Paris", "dist": 900, "transp": "driving-car"},
    {"adresse": "Gare de Lyon, Paris", "dist": 1800, "transp": "cycling-regular"}
  ],
  "nature": ["Lycée", "Collège"],
  "secteur": ["public"]
}
```

La forme historique à un seul point (`adresse`, `dist`, `transp` à la racine)
reste acceptée. La réponse contient `etablissements`, `isochrones` (une par
point de départ), `zone` et `zone_levels` (GeoJSON), `communes` (contour, score
de sécurité, fiscalité), `immobilier` (prix au m² par commune et par IRIS) et
`logements_sociaux`. Les structures sont décrites dans
[`internal/models`](internal/models/README.md).

### Facturation

| Méthode | Route | Auth | Description |
| ------- | ----- | :--: | ----------- |
| `GET` | `/api/billing` | ✓ | `{enabled, mode, balance_cents, free, currency, price_per_origin_cents, isochrone_price_cents, min_topup_cents, max_topup_cents, transactions}`. |
| `POST` | `/api/billing/topup` | ✓ | Crée un paiement Mollie (`{amount_cents}`) ; renvoie `{checkout_url, payment_id}`. |
| `POST` | `/api/billing/sync` | ✓ | Relit auprès de Mollie les paiements en cours du compte (retour de la page de paiement). |
| `POST` | `/api/billing/webhook/mollie` | — | Notification Mollie (`id=tr_…`) ; le statut est relu auprès de l'API Mollie. |

Une requête payante renvoie le nouveau solde dans l'en-tête `X-Credit-Balance`.

### Administration

| Méthode | Route | Auth | Description |
| ------- | ----- | :--: | ----------- |
| `GET` | `/api/admin/users` | admin | Liste de tous les comptes. |
| `PATCH` | `/api/admin/users/{id}` | admin | Change le niveau d'un compte (`{perms}`) ; un compte qui obtient l'accès est prévenu par e-mail. Impossible sur son propre compte (`422`). |
| `DELETE` | `/api/admin/users/{id}` | admin | Supprime un compte (`204`). Impossible sur son propre compte (`422`). |
| `POST` | `/api/admin/users/{id}/credit` | admin | Ajuste le solde (`{delta_cents, reason}` ; le solde ne devient jamais négatif). |
| `GET` | `/api/admin/users/{id}/transactions` | admin | Derniers mouvements de crédit d'un compte. |
| `GET` | `/api/admin/billing/settings` | admin | Tarifs et paramètres de facturation. |
| `PUT` | `/api/admin/billing/settings` | admin | Modifie `{price_per_origin_cents, isochrone_price_cents, welcome_credit_cents, min_topup_cents}`. |

## Comptes et authentification

L'authentification est **native** : à l'inscription, le backend génère un
identifiant (UUID v4), hache le mot de passe (PBKDF2-SHA256, 210 000
itérations) et envoie un lien de vérification `${PUBLIC_URL}/verify-email?token=…`
(usage unique, valable 24 h ; seul le hash SHA-256 du jeton est stocké).

Chaque compte a un niveau d'accès (`utilisateur.perms`, enum ordonné) :

| Niveau       | Signification                                        | Accès |
| ------------ | ---------------------------------------------------- | ----- |
| `unverified` | Compte créé, e-mail non vérifié (niveau initial).    | aucun |
| `verified`   | En attente de validation par un administrateur (comptes OIDC avec `OIDC_DEFAULT_PERMS=verified`, ou choix d'un administrateur). | aucun |
| `granted`    | Accès à l'application (atteint directement par la vérification de l'e-mail). | application |
| `admin`      | Administrateur.                                      | application + administration |

Seuls les comptes `granted` et `admin` reçoivent un token. Le niveau est relu en
base **à chaque requête** : rétrograder un compte lui retire l'accès
immédiatement.

Les JWT HS256 (audience `douceville`, validité 7 jours) sont signés avec
`JWT_SECRET` et portent un identifiant unique (`jti`). La déconnexion révoque ce
`jti` côté serveur (table `revoked_token`) jusqu'à l'expiration du token. Le
frontend stocke le token dans le `localStorage` et l'envoie en Bearer.

**Administration.** Les administrateurs disposent d'un panneau **Utilisateurs**
(icône dans la barre latérale) : liste des comptes (ceux en attente en tête),
changement de niveau, suppression, ajustement du crédit, tarifs. Un
administrateur ne peut ni changer son propre niveau ni supprimer son propre
compte. Le premier administrateur se désigne avec
`uv run scripts/set_user_perms.py set EMAIL admin`, ou en SQL :

```sql
UPDATE utilisateur SET perms = 'admin' WHERE lower(email) = lower('alice@example.com');
```

**Profil.** Un utilisateur modifie son compte en cliquant sur son avatar
(`avatar_url`, sinon Gravatar, sinon initiales). Un changement d'adresse e-mail
reste **en attente** jusqu'au clic sur le lien envoyé à la nouvelle adresse
(l'ancienne est prévenue et reste utilisable jusque-là).

### Connexion OpenID Connect (optionnelle)

Si `OIDC_ISSUER_URL` est défini, un bouton « Se connecter avec … » apparaît à
côté du formulaire. Le flux est un *authorization code* avec PKCE ; state, nonce
et vérificateur PKCE sont conservés dans un cookie signé de courte durée.
`/api/auth/oidc/login` redirige vers le fournisseur, qui revient sur
`/api/auth/oidc/callback` (URL à déclarer chez le fournisseur). Le backend
vérifie l'ID token puis émet **son propre JWT**.

- Une identité inconnue crée un compte (sans mot de passe) au niveau
  `OIDC_DEFAULT_PERMS`.
- Si un compte existe déjà avec le même e-mail, l'identité lui est rattachée
  uniquement si le fournisseur atteste l'adresse (`email_verified`) ; sinon la
  connexion est refusée.
- Les identités sont stockées dans la table `oidc_identity` (issuer, subject).

## Facturation (crédit prépayé, Mollie)

Si `BILLING_ENABLED=true`, chaque requête payante est débitée du **solde
prépayé** du compte (`utilisateur.balance_cents`), que l'utilisateur recharge
par [Mollie](https://www.mollie.com) depuis *Paramètres du compte → Crédit*.

- `POST /api/etablissements` coûte *prix par point de départ × nombre de points
  de départ* (chaque point est un appel OpenRouteService) ; `GET /api/isochrone`
  a son propre prix. L'autocomplétion d'adresse est gratuite.
- Le prix est débité **avant** les appels externes, de façon atomique (le solde
  ne peut pas devenir négatif) ; si la requête échoue, il est remboursé. Solde
  insuffisant : `402`, `code=insufficient_credit`.
- Chaque nouveau compte reçoit un **crédit de bienvenue** configurable.
- Les administrateurs ne sont jamais facturés. Ils règlent les tarifs, le crédit
  de bienvenue et le rechargement minimum (panneau *Utilisateurs → Tarifs*), et
  peuvent ajuster le solde d'un compte (avec un motif).
- Chaque mouvement est journalisé (`credit_transaction`) et visible par
  l'utilisateur.

**Mode test.** Le mode dépend uniquement de la clé : `MOLLIE_API_KEY=test_…`
simule les paiements (sur la page de test, on choisit *Paid*, *Failed*,
*Canceled*…) ; une clé `live_…` encaisse réellement. Le mode test est signalé
dans l'interface (badge « MODE TEST ») et dans les journaux.

**Webhook.** Mollie notifie `MOLLIE_WEBHOOK_URL`, qui doit être joignable depuis
Internet ; le statut est toujours relu auprès de l'API Mollie (les
notifications ne sont pas signées). En local (`localhost`), aucune webhook n'est
transmise : au retour de la page de paiement (`/?billing_return=1`), le frontend
appelle `/api/billing/sync`. Pour tester la webhook en local, exposer le serveur
(`cloudflared tunnel --url http://localhost:3566` ou `ngrok http 3566`) et
définir `PUBLIC_URL` en conséquence. Un paiement n'est crédité qu'une fois, même
si la notification est rejouée.

## Développement

Prérequis : Go 1.26+, Docker (pour PostGIS), [`uv`](https://docs.astral.sh/uv/)
pour les scripts Python.

```bash
./restart.sh               # PostGIS (docker compose, service « geotestdb »)
./launch_server.sh         # serveur sur http://localhost:3566

go build ./...
go vet ./...
go test ./...
```

- **Tests unitaires** — `go test ./...` ne nécessite ni base ni réseau : les
  handlers sont testés à travers le routeur avec des doublures (`mockStore`,
  `failingStore`, `fakeBill`, `mockIso`, `mockGeo`… dans
  `internal/api/*_test.go`), les clients HTTP externes avec des serveurs
  `httptest`, l'envoi d'e-mails avec un serveur SMTP factice.
- **Tests d'intégration** — les requêtes SQL et les migrations de
  `internal/store` sont testées sur une vraie base PostGIS. Chaque test crée
  sa propre base jetable sur le serveur désigné par `DV_TEST_DSN` (rôle avec
  le droit `CREATEDB`), puis la supprime ; sans `DV_TEST_DSN`, ils sont ignorés.
  ```bash
  docker run -d --rm --name dv-test-pg -e POSTGRES_USER=test -e POSTGRES_PASSWORD=test \
    -p 127.0.0.1:55432:5432 postgis/postgis:18-3.7
  DV_TEST_DSN=postgres://test:test@127.0.0.1:55432/test go test -cover ./...
  ```
- **Couverture** — environ 86 % des instructions avec les tests d'intégration
  (`go test -coverprofile=cov.out ./... && go tool cover -func=cov.out`).
  Toute évolution doit être accompagnée de tests : voir [`CLAUDE.md`](CLAUDE.md).
- **Frontend** — HTML/CSS/JS statique sans étape de build dans `web/`, embarqué
  à la compilation : modifier un fichier de `web/` nécessite de relancer
  `go run` / `go build`.
- **Version** — affichée dans le titre de l'interface, injectée à la
  compilation : `go build -ldflags "-X github.com/umaneldi/douceville/web.Version=v1.2.3" ./cmd/server`.
- **Journaux** — `log/slog` en texte sur la sortie standard ; chaque requête
  HTTP est journalisée avec son `request_id`.

La documentation (ce README, [`CLAUDE.md`](CLAUDE.md), [`schema.md`](schema.md)
et les `README.md` de chaque paquet de `internal/`) doit être mise à jour avec
chaque évolution du code : voir [`CLAUDE.md`](CLAUDE.md).

## Déploiement

L'image est construite à partir du [`Dockerfile`](Dockerfile) multi-stage
(binaire Go statique sur base `distroless/static`, utilisateur non root), qui
embarque l'UI et les migrations. L'argument de build `VERSION` fixe la version
affichée.

```bash
docker build --build-arg VERSION=v1.2.3 -t douceville .
docker run -p 3566:3566 \
  -e JWT_SECRET=... -e POSTGRES_HOST=... -e POSTGRES_PASSWORD=... \
  douceville
```

- **Intégration continue** — le workflow
  [`.github/workflows/docker-image.yml`](.github/workflows/docker-image.yml)
  s'exécute à **chaque push** (branche ou tag) : `go build`, `go vet`,
  `go test` (avec un service PostGIS pour les tests d'intégration), puis
  construction de l'image Docker. L'image n'est **publiée** sur GHCR que pour
  un tag commençant par `v` (ex. `v1.2.3` → `ghcr.io/<dépôt>:v1.2.3`, `1.2.3`,
  `1.2`, `latest`) :
  ```bash
  git tag v1.2.3 && git push origin v1.2.3
  ```
- **Docker Compose** — [`docker-compose.yml`](docker-compose.yml) définit la
  base `geotestdb` (PostGIS, données dans `.test_db/`) et l'application `app`
  (profil `app` : `docker compose --profile app up -d`), sur le réseau Traefik
  externe `$TRAEFIK_NETWORK`. Le conteneur écoute sur `3566` et sert l'UI
  **et** l'API.

## Sources de données

- Établissements : [adresse et géolocalisation des établissements du premier et second degré](https://data.education.gouv.fr/explore/dataset/fr-en-adresse-et-geolocalisation-etablissements-premier-et-second-degre/information)
- Résultats : [lycées GT](https://data.education.gouv.fr/explore/assets/fr-en-indicateurs-de-resultat-des-lycees-gt_v2/) ([ancienne série](https://data.education.gouv.fr/explore/assets/fr-en-indicateurs-de-resultat-des-lycees-denseignement-general-et-technologique/)),
  [lycées professionnels](https://data.education.gouv.fr/explore/assets/fr-en-indicateurs-de-resultat-des-lycees-pro_v2/) ([ancienne série](https://data.education.gouv.fr/explore/assets/fr-en-indicateurs-de-resultat-des-lycees-denseignement-professionnels/)),
  [collèges](https://data.education.gouv.fr/explore/assets/fr-en-indicateurs-valeur-ajoutee-colleges/)
- Délinquance : [bases statistiques communales de la délinquance enregistrée](https://www.data.gouv.fr/datasets/bases-statistiques-communale-departementale-et-regionale-de-la-delinquance-enregistree-par-la-police-et-la-gendarmerie-nationales?resource_id=604d71b8-337d-4869-9226-49e01bae87df)
- Communes : [code officiel géographique](https://www.data.gouv.fr/datasets/code-officiel-geographique-1?resource_id=eb836ef4-8be2-4a55-bfdb-eb1ad041933e)
  et [contours administratifs](https://etalab-datasets.geo.data.gouv.fr/contours-administratifs/2026/geojson/communes-50m.geojson.gz)
- Immobilier : [demandes de valeurs foncières géolocalisées](https://www.data.gouv.fr/datasets/demandes-de-valeurs-foncieres-geolocalisees?resource_id=d7933994-2c66-4131-a4da-cf7cd18040a4)
- Fiscalité : [fiscalité locale des particuliers](https://www.data.gouv.fr/datasets/fiscalite-locale-des-particuliers?resource_id=f48d0fcc-f732-445d-ba2d-886ec4952bce)
- Logements sociaux : [RPLS, données détaillées au logement](https://www.data.gouv.fr/datasets/donnees-detaillees-au-logement-du-repertoire-des-logements-locatifs-des-bailleurs-sociaux-rpls)
- Revenus (pas encore importés) : [revenus et pauvreté des ménages (Filosofi)](https://www.data.gouv.fr/datasets/revenus-et-pauvrete-des-menages-aux-niveaux-national-et-local-revenus-localises-sociaux-et-fiscaux)
