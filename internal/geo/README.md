# Paquet `geo`

Clients des services de géolocalisation externes : **routage** (isochrones et
itinéraires) par **OpenRouteService** ou **Valhalla** auto-hébergé — au choix
par `ROUTING_PROVIDER`, les deux clients implémentant les interfaces
`api.Isochroner` et `api.RoutePlanner` —, et **addok / Base Adresse Nationale**
pour le géocodage et l'autocomplétion d'adresses. Plus quelques utilitaires de
conversion en WKT.

## Fichiers

| Fichier | Contenu |
| ------- | ------- |
| `ors.go` | `NewORSClient(clé, racine, http)` : racine `DefaultORSURL` (API publique) si vide, sinon un ORS auto-hébergé. `ORSClient.CalcIsochrone` : isochrones emboîtées autour d'un point `[lon, lat]` pour un temps maximal et un pas donnés (`GET /api/isochrone`). `ORSClient.CalcIsochrones` : isochrone d'un seul temps pour chaque position d'une liste, par requêtes de `orsMaxLocations` (5) positions, résultats remis dans l'ordre grâce à `group_index` (recherche par établissements). `ORSClient.CalcItineraire` : itinéraire entre deux positions (`POST …/v2/directions/{profil}/geojson`) → tracé, distance (m) et durée (s). Profils `driving-car` par défaut, `cycling-regular`, `foot-walking`. |
| `valhalla.go` | `ValhallaClient` (`NewValhallaClient(url, concurrence, http)`) : mêmes méthodes, sur `POST /isochrone` et `POST /route`. Profils traduits en modèles de coût (`valhallaCosting` : `auto`, `bicycle`, `pedestrian`), temps convertis en **minutes**. `CalcIsochrone` : un contour par pas (`polygons`, `denoise` 1, `generalize` 20 m ; anneau extérieur du `Polygon`, ou du plus grand polygone d'un `MultiPolygon`). `CalcIsochrones` : Valhalla fusionne les positions d'une requête, donc **une requête par position**, au plus `concurrency` à la fois (`errgroup.SetLimit`), ordre conservé. `CalcItineraire` : longueur en km → m, tracé encodé décodé par `decodePolyline6` (précision 1e-6). Erreurs : `valhalla returned status N: <error>`. |
| `wkt.go` | `PolygonWKT` / `RingWKT` : anneau → `POLYGON((…))`. |
| `ban.go` | `BANClient` : `Geocode` (adresse → coordonnées) et `Suggest` (autocomplétion). Cible `http://<ADDOK_HOST>:7878`, ou l'API publique `https://data.geopf.fr/geocodage` si l'hôte est vide. |

## Dépendances

`internal/models` (types `Isochrone`, `Itineraire`), `golang.org/x/sync/errgroup`
(requêtes Valhalla parallèles) et la bibliothèque standard.

## Points d'attention

- Les coordonnées sont toujours dans l'ordre **`[lon, lat]`** (GeoJSON / WKT).
- Les temps (`dist`, `interval`) sont en **secondes**, malgré le nom `dist`
  hérité de l'ancien backend.
- Les noms de profils de l'API (`models.Transports`) restent ceux
  d'OpenRouteService ; `valhallaCosting` les traduit pour Valhalla (un nouveau
  mode s'ajoute aux deux).
- Le service Valhalla lit ses tuiles à la demande sur S3 (voir la section
  *Valhalla* du `README.md` racine) ; sa carte de connectivité doit rester
  désactivée, sinon tout itinéraire échoue tant que le cache est vide.
- ORS renvoie du GeoJSON : l'en-tête `Accept` doit accepter
  `application/geo+json` (sinon `406`).
- Les niveaux d'une isochrone sont triés par temps croissant ; `Geometry` est
  celle du temps maximal. `CalcIsochrones` ne demande qu'un temps (pas de
  niveaux) et échoue si une position n'a pas de géométrie dans la réponse.
- Chaque isochrone calculée est facturée à l'utilisateur ; celles des
  recherches sont mises en cache en base (voir `internal/api` et
  `internal/store`).

## Tests

`go test ./internal/geo/` — clients ORS (`ors_test.go`, dont la racine
configurable), Valhalla (`valhalla_test.go` : corps envoyés, `Polygon` /
`MultiPolygon`, ordre et limite de concurrence, erreurs, itinéraire et
`decodePolyline6` avec l'encodeur de test `encodePolyline6`) et BAN (`ban_test.go`)
contre des serveurs `httptest`. Les URL fixes (API publique ORS) ou dérivées
d'un nom d'hôte (addok) sont redirigées vers le serveur de test par le helper
`redirect`, un `http.Client` dont le transport réécrit l'hôte. Couvre aussi
les erreurs (statut HTTP, JSON invalide, réponse vide, serveur injoignable),
le découpage en lots de `CalcIsochrones` (7 positions → 2 requêtes, réponse
dans le désordre, géométrie manquante, `group_index` invalide) et la
conversion WKT, ainsi que `CalcItineraire` (URL, corps, clé, décodage,
profil par défaut, erreurs).
