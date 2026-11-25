# Paquet `geo`

Clients des services de géolocalisation externes : **OpenRouteService** pour
les isochrones et les itinéraires, et **addok / Base Adresse Nationale** pour le géocodage et
l'autocomplétion d'adresses. Plus quelques utilitaires de conversion en WKT.

## Fichiers

| Fichier | Contenu |
| ------- | ------- |
| `ors.go` | `ORSClient.CalcIsochrone` : isochrones emboîtées autour d'un point `[lon, lat]` pour un temps maximal et un pas donnés (`GET /api/isochrone`). `ORSClient.CalcIsochrones` : isochrone d'un seul temps pour chaque position d'une liste, par requêtes de `orsMaxLocations` (5) positions, résultats remis dans l'ordre grâce à `group_index` (recherche par établissements). `ORSClient.CalcItineraire` : itinéraire entre deux positions (`POST …/v2/directions/{profil}/geojson`) → tracé, distance (m) et durée (s). Profils `driving-car` par défaut, `cycling-regular`, `foot-walking`. `PolygonWKT` / `RingWKT` : anneau → `POLYGON((…))`. |
| `ban.go` | `BANClient` : `Geocode` (adresse → coordonnées) et `Suggest` (autocomplétion). Cible `http://<ADDOK_HOST>:7878`, ou l'API publique `https://data.geopf.fr/geocodage` si l'hôte est vide. |

## Dépendances

`internal/models` (type `Isochrone`) et la bibliothèque standard.

## Points d'attention

- Les coordonnées sont toujours dans l'ordre **`[lon, lat]`** (GeoJSON / WKT).
- Les temps (`dist`, `interval`) sont en **secondes**, malgré le nom `dist`
  hérité de l'ancien backend.
- ORS renvoie du GeoJSON : l'en-tête `Accept` doit accepter
  `application/geo+json` (sinon `406`).
- Les niveaux d'une isochrone sont triés par temps croissant ; `Geometry` est
  celle du temps maximal. `CalcIsochrones` ne demande qu'un temps (pas de
  niveaux) et échoue si une position n'a pas de géométrie dans la réponse.
- Chaque isochrone calculée est facturée à l'utilisateur ; celles des
  recherches sont mises en cache en base (voir `internal/api` et
  `internal/store`).

## Tests

`go test ./internal/geo/` — clients ORS (`ors_test.go`) et BAN (`ban_test.go`)
contre des serveurs `httptest`. Les URL fixes (API publique ORS) ou dérivées
d'un nom d'hôte (addok) sont redirigées vers le serveur de test par le helper
`redirect`, un `http.Client` dont le transport réécrit l'hôte. Couvre aussi
les erreurs (statut HTTP, JSON invalide, réponse vide, serveur injoignable),
le découpage en lots de `CalcIsochrones` (7 positions → 2 requêtes, réponse
dans le désordre, géométrie manquante, `group_index` invalide) et la
conversion WKT, ainsi que `CalcItineraire` (URL, corps, clé, décodage,
profil par défaut, erreurs).
