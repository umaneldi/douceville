# Paquet `api`

Couche HTTP du backend : routeur, handlers de l'API JSON (`/api`) et service
de l'interface web embarquée (`/`). C'est le seul paquet qui orchestre les
autres (`auth`, `billing`, `geo`, `mail`, `store`).

## Fichiers

| Fichier | Contenu |
| ------- | ------- |
| `router.go` | `NewRouter` : montage des routes chi (publiques, protégées par `auth.Middleware`, admin par `auth.RequireAdmin`), service de l'application monopage (`spaHandler`, injection de la version dans `index.html`), helpers `writeJSON` / `writeError`. |
| `handlers.go` | Struct `Handlers` (dépendances) et interfaces `Isochroner`, `Geocoder`, `AddressSuggester`, `OIDCProvider` ; recherche dans la zone (`POST /etablissements`), détail d'un établissement, isochrone, profil, autocomplétion d'adresse. |
| `levels.go` | Calcul des niveaux de temps emboîtés de la zone (`zoneLevelWKTs`) à partir des isochrones de chaque point de départ. |
| `auth_handlers.go` | Inscription, vérification d'e-mail, renvoi du lien, connexion, déconnexion, flux OIDC (config, redirection, callback). |
| `user_handlers.go` | Profil (`PATCH /user`), changement d'e-mail et de mot de passe. |
| `admin_handlers.go` | Gestion des comptes : liste, changement de niveau (avec e-mail d'approbation), suppression. |
| `billing_handlers.go` | Facturation : `charge` (débit/remboursement d'une requête payante), solde, rechargement Mollie, synchronisation, webhook, tarifs et ajustements admin. |
| `logging.go` | `logger(ctx)` (logger enrichi du `request_id`) et middleware `requestLogger`. |

## Fonctionnement

- `Handlers` reçoit toutes ses dépendances par **interfaces**, assemblées dans
  `cmd/server/main.go`. `OIDC` et `Billing` valent `nil` quand la
  fonctionnalité est désactivée.
- **Recherche** (`etablissementsInZone`) : débit du prix → géocodage et
  isochrone de chaque origine en parallèle (`errgroup`) → intersection des
  isochrones par PostGIS (`Store.IntersectZones`) pour la zone et chaque niveau
  → lecture des établissements, communes, prix immobiliers et logements
  sociaux de la zone → étiquetage du niveau de chaque point
  (`tagLevels` / `Store.PointLevels`). Au plus `maxOrigines` (10) points de
  départ.
- **Facturation** : `charge` débite atomiquement *avant* les appels externes et
  renvoie une fonction `refund` à appeler si la requête échoue ; le nouveau
  solde est renvoyé dans l'en-tête `X-Credit-Balance`. Sans facturation, ou
  pour un administrateur, `charge` ne fait rien.
- **SPA** : toute route hors `/api` qui ne correspond à aucun fichier de `web/`
  renvoie `index.html` (ex. `/verify-email?token=…`).

## Dépendances

`internal/auth`, `internal/billing`, `internal/geo`, `internal/mail`,
`internal/models`, `internal/store`, `web`, `github.com/go-chi/chi/v5`,
`golang.org/x/sync/errgroup`.

## Points d'attention

- Une nouvelle route doit être ajoutée dans `router.go` **et** dans le tableau
  de l'API du `README.md` racine.
- Les codes d'erreur (`email_not_verified`, `invalid_password`,
  `insufficient_credit`…) sont lus par `web/app.js` : ne pas les renommer sans
  adapter le frontend.
- Un administrateur ne peut ni changer son propre niveau ni supprimer son propre
  compte (`422`), pour qu'il reste toujours au moins un administrateur.
- Le webhook Mollie n'est pas signé : le statut d'un paiement est toujours relu
  auprès de l'API du prestataire.

## Tests

`go test ./internal/api/` — sans base ni réseau, grâce aux doublures définies
dans les fichiers de test :

| Fichier | Contenu |
| ------- | ------- |
| `handlers_test.go` | `mockStore` (implémentation complète de `store.Store`), `mockIso`, `mockGeo`, `mockSuggest` ; recherche, isochrone, autocomplétion. |
| `user_handlers_test.go` | Helpers `alice` (compte de test), `call` (requête authentifiée à travers le routeur), `decodeMap` ; profil, e-mail, mot de passe. |
| `auth_handlers_test.go` | `fakeMailer` ; inscription, connexion, vérification, déconnexion. |
| `oidc_handlers_test.go` | Flux OIDC avec un `OIDCProvider` factice (`stubOIDC`). |
| `admin_handlers_test.go` | `adminStore` ; gestion des comptes. |
| `billing_mock_test.go` / `billing_handlers_test.go` | `fakeBill` (soldes en mémoire), `fakeProvider` (Mollie simulé), helper `billed` ; facturation. |
| `failures_test.go` | `failingStore` (fait échouer une méthode nommée du `Store`), `callAs`, `failMailer`, `errProvider` ; chemins d'erreur de toutes les routes, remboursement après échec. |
| `spa_test.go` | Service de l'application monopage. |

Toute nouvelle méthode de `store.Store` doit être ajoutée à `mockStore` (et à
`failingStore` si un handler doit gérer son échec). Toute nouvelle route doit
être testée : cas nominal, validation, droits et erreurs des dépendances.
