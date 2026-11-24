# Paquet `auth`

Authentification native du backend : émission et validation des JWT,
middleware HTTP de protection des routes, hachage des mots de passe, jetons de
vérification d'e-mail et connexion OpenID Connect.

## Fichiers

| Fichier | Contenu |
| ------- | ------- |
| `jwt.go` | `IssueToken` / `ParseToken` : JWT HS256 signés avec `JWT_SECRET`, audience `douceville`, claims `sub`, `email`, `jti`, `iat`, `exp`. Durée par défaut `DefaultTokenTTL` (7 jours). |
| `middleware.go` | `Middleware(secret, users)` : exige un Bearer valide, non révoqué, d'un compte au moins `granted` ; attache l'utilisateur et les claims au contexte (`UserFromContext`, `ClaimsFromContext`). `RequireAdmin` restreint au niveau `admin`. Interface `UserLookup`. |
| `password.go` | `HashPassword` / `VerifyPassword` : PBKDF2-HMAC-SHA256, 210 000 itérations, sel de 16 octets, format `pbkdf2_sha256$iter$sel$hash`. |
| `verification.go` | `NewVerificationToken` / `HashVerificationToken` : jeton aléatoire envoyé par e-mail, seul son SHA-256 est stocké. Validité `VerificationTokenTTL` (24 h). |
| `oidc.go` | `NewOIDC` / type `OIDC` : flux *authorization code* + PKCE (découverte du fournisseur, `AuthURL`, `Exchange`). State, nonce et vérificateur PKCE sont conservés dans un cookie JWT signé (`dv_oidc`, 10 min, chemin `/api/auth/oidc`). |

## Codes de réponse du middleware

| Cas | Statut | `code` |
| --- | :----: | ------ |
| Header `Authorization` absent ou non Bearer | 403 | — |
| Token invalide, expiré, révoqué ou utilisateur inconnu | 401 | — |
| Compte sous le niveau `granted` | 403 | `account_not_granted` |
| Route admin appelée par un non-admin | 403 | `admin_required` |

## Dépendances

`internal/models`, `github.com/golang-jwt/jwt/v5`,
`github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2`. Le paquet ne dépend pas
de `store` : le middleware utilise l'interface `UserLookup` (implémentée par
`store.PgStore`).

## Points d'attention

- Le niveau d'accès est **relu en base à chaque requête** : un retrait d'accès
  est immédiat, sans attendre l'expiration du token.
- Un token sans `jti` est refusé (il ne pourrait pas être révoqué).
- Le `login` exposé est l'e-mail courant du compte (il a pu changer depuis
  l'émission du token).
- Le cookie OIDC est `Secure` lorsque `PUBLIC_URL` est en `https://`.

## Tests

`go test ./internal/auth/` — JWT, mots de passe, middleware (avec un
`UserLookup` factice), flux OIDC (fournisseur simulé avec `httptest`), jetons
de vérification et accesseurs de contexte (`verification_test.go`).
