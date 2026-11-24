# Paquet `geo`

Clients des services de géolocalisation externes : **OpenRouteService** pour
les isochrones et **addok / Base Adresse Nationale** pour le géocodage et
l'autocomplétion d'adresses. Plus quelques utilitaires de conversion en WKT.

## Fichiers

| Fichier | Contenu |
| ------- | ------- |
| `ors.go` | `ORSClient.CalcIsochrone` : isochrones emboîtées autour d'un point `[lon, lat]` pour un temps maximal et un pas donnés (profils `driving-car` par défaut, `cycling-regular`, `foot-walking`…). `LevelInterval` : pas de 5 min, ou 10 min au-delà de 50 min (ORS limite une requête à 10 isochrones). `PolygonWKT` / `RingWKT` : anneau → `POLYGON((…))`. |
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
  celle du temps maximal.
- Chaque appel ORS est facturé à l'utilisateur (voir `internal/api`).

## Tests

`go test ./internal/geo/` — clients ORS (`ors_test.go`) et BAN (`ban_test.go`)
contre des serveurs `httptest`. Les URL fixes (API publique ORS) ou dérivées
d'un nom d'hôte (addok) sont redirigées vers le serveur de test par le helper
`redirect`, un `http.Client` dont le transport réécrit l'hôte. Couvre aussi
les erreurs (statut HTTP, JSON invalide, réponse vide, serveur injoignable),
`LevelInterval` et la conversion WKT.
