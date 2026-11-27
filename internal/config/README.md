# Paquet `config`

Chargement et validation de la configuration du service depuis les
**variables d'environnement**. Seul `cmd/server` l'utilise : les autres paquets
reçoivent leurs paramètres explicitement (structs de config, arguments).

## Fichiers

| Fichier | Contenu |
| ------- | ------- |
| `config.go` | Struct `Config`, `Load()` (lecture + valeurs par défaut + validation), `DatabaseURL()` (DSN PostgreSQL), `OIDCEnabled()`. |

## Validation effectuée par `Load`

- `JWT_SECRET` est obligatoire.
- `SMTP_FROM` est obligatoire si `SMTP_HOST` est défini.
- Si `OIDC_ISSUER_URL` est défini : `OIDC_CLIENT_ID` obligatoire,
  `OIDC_DEFAULT_PERMS` parmi `unverified`, `verified`, `granted`, `admin`, et
  le scope `openid` est ajouté s'il manque.
- Si `BILLING_ENABLED` est vrai (`1`, `true`, `yes`, `on`) : `MOLLIE_API_KEY`
  doit commencer par `test_` ou `live_`.
- `ROUTING_PROVIDER` vaut `ors` (défaut) ou `valhalla` (`RoutingORS`,
  `RoutingValhalla`) ; `OPENROUTESERVICE_URL` et `VALHALLA_URL` doivent être des
  URL `http(s)` ; `VALHALLA_CONCURRENCY` (défaut 8) un entier strictement
  positif ; `SOURCES_PAR_NATURE` (défaut 10 avec `ors`, 0 — sans plafond — avec
  `valhalla`) et `SEARCH_COST_WARNING_CENTS` (défaut 200) des entiers positifs
  ou nuls (`nonNegativeInt`) — voir `loadRouting`.
- `ISOCHRONE_CACHE_DAYS` (défaut 30, converti en `IsochroneCacheTTL`) et
  `ISOCHRONE_CACHE_MAX` (défaut 20 000, `IsochroneCacheMax`) doivent être des
  entiers strictement positifs (`positiveInt`).

Valeurs dérivées : `PUBLIC_URL` est privé de son `/` final ;
`OIDC_REDIRECT_URL` et `MOLLIE_WEBHOOK_URL` ont pour défaut une URL construite
sur `PUBLIC_URL`. Une variable définie mais **vide** prend sa valeur par défaut.

La liste complète des variables et de leurs défauts est tenue dans le
`README.md` racine (section *Configuration*).

## Dépendances

Bibliothèque standard uniquement.

## Points d'attention

Ajouter une variable implique de mettre à jour, dans le même commit :
`Config` et `Load`, `config_test.go`, `sample.env`, la section `environment`
de `docker-compose.yml` et le tableau *Configuration* du `README.md`.

## Tests

`go test ./internal/config/` — valeurs par défaut et URL dérivées,
`DatabaseURL`, validation de `JWT_SECRET`, SMTP, OIDC, facturation et cache
d'isochrones. Le
helper `clearEnv` vide toutes les variables lues, pour que les tests ne
dépendent pas de l'environnement du développeur : toute nouvelle variable doit
y être ajoutée.
