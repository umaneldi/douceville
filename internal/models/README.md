# Paquet `models`

Structures de données partagées : corps de requête et de réponse de l'API
(sérialisés en JSON) et types échangés entre paquets. Il ne contient pas de
logique métier, hormis le classement des niveaux d'accès.

## Fichiers

| Fichier | Contenu |
| ------- | ------- |
| `models.go` | Niveaux d'accès `Perms` (`AtLeast`, `Valid`) ; utilisateurs (`DvUser`, `UserRecord`, `OIDCIdentity`) ; corps des routes d'auth et de compte (`Credentials`, `AuthResponse`, `ProfileUpdate`…) ; recherche (`QueryParameters`, `Origine`, `Isochrone`, `IsoLevel`, `ZoneLevel`, `ListeEtablissementsAvecIsochrone`) ; données de la zone (`Etablissement`, `Resultat`, `CommuneSecurite`, `Fiscalite`, `ImmobilierZone`, `QuartierImmobilier`, `CommuneImmobilier`, `PrixM2`, `LogementSocialPosition`, `LogementSocialGroupe`). |
| `billing.go` | Facturation : `BillingSettings`, `CreditTransaction` et ses types (`topup`, `usage`, `refund`, `admin_adjust`, `welcome`), `Payment`, `TopupRequest`, `CreditAdjustment`. |

## Dépendances

Aucune dépendance interne : tous les autres paquets peuvent l'importer sans
risque de cycle.

## Points d'attention

- Les **noms de champs JSON** sont un contrat avec le frontend (`web/app.js`)
  et reprennent ceux de l'ancien backend Python : ne pas les renommer sans
  adapter le frontend.
- `UserRecord` contient le hash du mot de passe (`json:"-"`) : il ne doit
  jamais être renvoyé au client ; utiliser `DvUser`.
- Les niveaux `Perms` sont **ordonnés** (`unverified` < `verified` <
  `granted` < `admin`) ; comparer avec `AtLeast`, jamais par égalité de
  chaînes. Un niveau inconnu n'atteint aucun seuil.
- Les géométries lues en base (`Position`, `Contour`, `Zone`…) sont du GeoJSON
  brut (`json.RawMessage`) transmis tel quel ; les anneaux d'isochrone sont des
  `[][2]float64` en `[lon, lat]`.
- Montants en **centimes** (`int64`), temps de trajet en **secondes**.

## Tests

`go test ./internal/models/` — noms des champs JSON (contrat avec le frontend),
ordre et validité des niveaux d'accès.
