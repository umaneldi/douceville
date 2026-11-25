package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/umaneldi/douceville/internal/models"
)

// etabColumnsE liste les colonnes d'un établissement (alias e) suivies de son
// taux de réussite (alias t, vue etablissement_taux) : voir scanEtablissementTaux.
const etabColumnsE = `e.id, e."UAI", e.nom, e.adresse, e.lieu_dit, e.code_postal, e.commune,
	ST_AsGeoJSON(e.position) AS position, e.departement, e.academie, e.secteur, e.ouverture, e.nature, e.filieres, t.taux`

// scanEtablissementTaux lit les colonnes etabColumnsE, suivies de extra.
func scanEtablissementTaux(row pgx.Row, extra ...any) (*models.Etablissement, error) {
	var e models.Etablissement
	dest := []any{
		&e.ID, &e.UAI, &e.Nom, &e.Adresse, &e.LieuDit, &e.CodePostal, &e.Commune,
		&e.Position, &e.Departement, &e.Academie, &e.Secteur, &e.Ouverture, &e.Nature, &e.Filieres, &e.Taux,
	}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return nil, err
	}
	return &e, nil
}

// secteurSep sépare les secteurs (et les filières) d'un critère, transmis en une seule chaîne
// (PostgreSQL n'accepte pas de tableaux de tableaux de tailles différentes).
const secteurSep = "|"

// criteresCTE déclare la CTE c (une ligne par critère : nature, secteurs,
// filieres, tmin, tmax, k) à partir des paramètres $n..$n+4 de criteresArgs.
func criteresCTE(n int) string {
	return fmt.Sprintf(`c AS (
		SELECT * FROM unnest($%d::text[], $%d::text[], $%d::text[], $%d::float8[], $%d::float8[])
			WITH ORDINALITY AS c(nature, secteurs, filieres, tmin, tmax, k)
	)`, n, n+1, n+2, n+3, n+4)
}

// critereMatch est la condition « l'établissement e (taux t.taux) satisfait le
// critère c » : même nature (une école primaire satisfait aussi les critères
// maternelle et élémentaire), secteur parmi ceux du critère (tous si aucun),
// au moins une filière parmi celles du critère (pas de filtre si aucune),
// taux dans la plage du critère (non filtré si inconnu).
var critereMatch = `(c.nature = e.nature OR (e.nature = '` + models.NaturePrimaire + `'
		AND c.nature IN ('` + strings.Join(models.NaturesPrimaire, "', '") + `')))
	AND (c.secteurs = '' OR e.secteur = ANY (string_to_array(c.secteurs, '` + secteurSep + `')))
	AND (c.filieres = '' OR e.filieres && string_to_array(c.filieres, '` + secteurSep + `'))
	AND (t.taux IS NULL OR t.taux BETWEEN c.tmin AND c.tmax)`

// criteresArgs convertit les critères en tableaux parallèles pour criteresCTE.
func criteresArgs(criteres []models.CritereNature) []any {
	natures := make([]string, len(criteres))
	secteurs := make([]string, len(criteres))
	filieres := make([]string, len(criteres))
	tmin := make([]float64, len(criteres))
	tmax := make([]float64, len(criteres))
	for i, c := range criteres {
		natures[i] = c.Nature
		secteurs[i] = strings.Join(c.Secteur, secteurSep)
		filieres[i] = strings.Join(c.Filiere, secteurSep)
		tmin[i], tmax[i] = c.TauxMin, c.TauxMax
	}
	return []any{natures, secteurs, filieres, tmin, tmax}
}

// loadResultats charge les résultats de chaque établissement.
func (s *PgStore) loadResultats(ctx context.Context, etabs []models.Etablissement) error {
	for i := range etabs {
		res, err := s.resultatsForUAI(ctx, etabs[i].UAI)
		if err != nil {
			return err
		}
		etabs[i].Resultats = res
	}
	return nil
}

// metresParDegre est la longueur d'un degré de latitude (approchée par excès
// pour que la boîte englobante contienne tout le disque).
const metresParDegre = 110000.0

// SelectSourceEtablissements implémente Store.SelectSourceEtablissements.
// La boîte englobante (en degrés, sur etablissement.position en SRID 0) sert
// l'index GiST ; ST_DWithin sur geography donne le disque exact.
func (s *PgStore) SelectSourceEtablissements(ctx context.Context, centre [2]float64, rayonM float64, criteres []models.CritereNature, parNature int) ([]models.Source, error) {
	if len(criteres) == 0 || parNature <= 0 {
		return []models.Source{}, nil
	}
	dLat := rayonM / metresParDegre
	dLon := dLat / math.Max(math.Cos(centre[1]*math.Pi/180), 0.01)

	// Établissements du disque (cand), puis leur taux (ct), matérialisés : sans
	// cela, le planificateur parcourt l'agrégat de etablissement_taux pour
	// chaque candidat (secondes au lieu de ~100 ms sur la base complète).
	q := `WITH ` + criteresCTE(6) + `,
		p AS (SELECT ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography AS g),
		cand AS MATERIALIZED (
			SELECT e.*, ST_Distance(ST_SetSRID(e.position, 4326)::geography, p.g) AS distance
			FROM etablissement e CROSS JOIN p
			WHERE e.position && ST_MakeEnvelope($1 - $3, $2 - $4, $1 + $3, $2 + $4)
				AND ST_DWithin(ST_SetSRID(e.position, 4326)::geography, p.g, $5)
		),
		ct AS MATERIALIZED (
			SELECT cand.*, et.taux FROM cand LEFT JOIN etablissement_taux et ON et.uai = cand."UAI"
		)
		SELECT ` + etabColumnsSel + `, s.k FROM (
			SELECT ` + etabColumnsE + `, c.k,
				row_number() OVER (PARTITION BY c.k ORDER BY t.taux DESC NULLS LAST, e.distance, e."UAI") AS rang
			FROM ct e
			CROSS JOIN LATERAL (SELECT e.taux) t
			JOIN c ON ` + critereMatch + `
		) s
		WHERE s.rang <= $11
		ORDER BY s.k, s.rang`

	args := append([]any{centre[0], centre[1], dLon, dLat, rayonM}, criteresArgs(criteres)...)
	args = append(args, parNature)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sources := []models.Source{}
	for rows.Next() {
		var k int
		e, err := scanEtablissementTaux(rows, &k)
		if err != nil {
			return nil, err
		}
		c := criteres[k-1]
		sources = append(sources, models.Source{Etablissement: *e, Critere: c.Nature, Dist: c.Dist, Transp: c.Transp})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range sources {
		res, err := s.resultatsForUAI(ctx, sources[i].UAI)
		if err != nil {
			return nil, err
		}
		sources[i].Resultats = res
	}
	return sources, nil
}

// etabColumnsSel relit, depuis la sous-requête s, les colonnes de etabColumnsE.
const etabColumnsSel = `s.id, s."UAI", s.nom, s.adresse, s.lieu_dit, s.code_postal, s.commune,
	s.position, s.departement, s.academie, s.secteur, s.ouverture, s.nature, s.filieres, s.taux`

// FindEtablissementsInZone implémente Store.FindEtablissementsInZone.
func (s *PgStore) FindEtablissementsInZone(ctx context.Context, polygonWKT string, criteres []models.CritereNature) ([]models.Etablissement, error) {
	q := `SELECT ` + etabColumnsE + `
		FROM etablissement e
		LEFT JOIN etablissement_taux t ON t.uai = e."UAI"
		WHERE ST_Within(e.position, ST_GeomFromEWKT($1))`
	args := []any{polygonWKT}
	if len(criteres) > 0 {
		q = `WITH ` + criteresCTE(2) + ` ` + q + ` AND EXISTS (SELECT 1 FROM c WHERE ` + critereMatch + `)`
		args = append(args, criteresArgs(criteres)...)
	}
	q += ` ORDER BY e.nature, e."UAI"`

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	etabs := []models.Etablissement{}
	for rows.Next() {
		e, err := scanEtablissementTaux(rows)
		if err != nil {
			return nil, err
		}
		etabs = append(etabs, *e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return etabs, s.loadResultats(ctx, etabs)
}

// prixMedian est l'expression du prix au m² médian des ventes i.
const prixMedian = `percentile_cont(0.5) WITHIN GROUP (ORDER BY i.valeur_fonciere / i.surface_reelle_bati)`

// FilterZone implémente Store.FilterZone. Le prix d'un IRIS sans vente est
// celui de sa commune (toutes ses ventes) ; sécurité et fiscalité sont celles
// de la dernière année disponible.
func (s *PgStore) FilterZone(ctx context.Context, polygonWKT string, f models.FiltresZone) (string, json.RawMessage, bool, error) {
	const q = `WITH z AS (SELECT ST_SetSRID(ST_GeomFromText($1), 4326) AS g),
		qz AS (
			SELECT q.iris, q.code_commune, q.contour FROM quartier q, z
			WHERE ST_Intersects(q.contour, z.g)
		),
		pi AS (
			SELECT i.iris, ` + prixMedian + ` AS prix FROM immobilier i
			WHERE i.iris IN (SELECT iris FROM qz) GROUP BY i.iris
		),
		pc AS (
			SELECT q.code_commune, ` + prixMedian + ` AS prix
			FROM quartier q JOIN immobilier i ON i.iris = q.iris
			WHERE q.code_commune IN (SELECT code_commune FROM qz) GROUP BY q.code_commune
		),
		ok AS (
			SELECT qz.contour FROM qz
			LEFT JOIN pi ON pi.iris = qz.iris
			LEFT JOIN pc ON pc.code_commune = qz.code_commune
			LEFT JOIN securite_score s ON s.code_commune = qz.code_commune
				AND s.annee = (SELECT max(annee) FROM securite_score)
			LEFT JOIN fiscalite f ON f.code_commune = qz.code_commune
				AND f.year = (SELECT max(year) FROM fiscalite)
			WHERE ($2::float8 IS NULL OR coalesce(pi.prix, pc.prix) >= $2)
				AND ($3::float8 IS NULL OR coalesce(pi.prix, pc.prix) <= $3)
				AND ($4::float8 IS NULL OR s.score >= $4)
				AND ($5::float8 IS NULL OR f.taux_global_tfb + coalesce(f.taux_plein_teom, 0) <= $5)
		),
		r AS (
			SELECT ST_CollectionExtract(ST_Intersection(z.g, (SELECT ST_Union(contour) FROM ok)), 3) AS g FROM z
		)
		SELECT coalesce(ST_IsEmpty(g), true), coalesce(ST_AsText(g), ''), coalesce(ST_AsGeoJSON(g, 6), '') FROM r`

	var empty bool
	var wkt, gj string
	if err := s.pool.QueryRow(ctx, q, polygonWKT, f.PrixM2Min, f.PrixM2Max, f.SecuriteMin, f.FiscaliteMax).
		Scan(&empty, &wkt, &gj); err != nil {
		return "", nil, true, err
	}
	if empty {
		return "", nil, true, nil
	}
	return wkt, json.RawMessage(gj), false, nil
}

// keyArgs convertit des clés de cache en tableaux parallèles.
func keyArgs(keys []models.IsochroneKey) (lons, lats, dists []int32, transps []string) {
	for _, k := range keys {
		lons = append(lons, int32(k.LonE5))
		lats = append(lats, int32(k.LatE5))
		dists = append(dists, int32(k.Dist))
		transps = append(transps, k.Transp)
	}
	return
}

// GetCachedIsochrones implémente Store.GetCachedIsochrones : les entrées
// trouvées sont marquées utilisées (last_used_at).
func (s *PgStore) GetCachedIsochrones(ctx context.Context, keys []models.IsochroneKey, maxAge time.Duration) (map[models.IsochroneKey][][2]float64, error) {
	out := map[models.IsochroneKey][][2]float64{}
	if len(keys) == 0 {
		return out, nil
	}
	lons, lats, dists, transps := keyArgs(keys)
	rows, err := s.pool.Query(ctx, `UPDATE isochrone_cache c SET last_used_at = now()
		FROM unnest($1::int[], $2::int[], $3::int[], $4::text[]) AS k(lon, lat, dist, transp)
		WHERE c.lon_e5 = k.lon AND c.lat_e5 = k.lat AND c.dist = k.dist AND c.transp = k.transp
			AND c.created_at > now() - make_interval(secs => $5)
		RETURNING c.lon_e5, c.lat_e5, c.dist, c.transp, c.ring`,
		lons, lats, dists, transps, maxAge.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k models.IsochroneKey
		var ring [][2]float64
		if err := rows.Scan(&k.LonE5, &k.LatE5, &k.Dist, &k.Transp, &ring); err != nil {
			return nil, err
		}
		out[k] = ring
	}
	return out, rows.Err()
}

// PutCachedIsochrones implémente Store.PutCachedIsochrones.
func (s *PgStore) PutCachedIsochrones(ctx context.Context, entries map[models.IsochroneKey][][2]float64, maxAge time.Duration, maxEntries int) error {
	keys := make([]models.IsochroneKey, 0, len(entries))
	rings := make([]string, 0, len(entries))
	for k, ring := range entries {
		b, err := json.Marshal(ring)
		if err != nil {
			return err
		}
		keys = append(keys, k)
		rings = append(rings, string(b))
	}
	lons, lats, dists, transps := keyArgs(keys)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // sans effet après Commit

	if len(keys) > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO isochrone_cache (lon_e5, lat_e5, dist, transp, ring)
			SELECT * FROM unnest($1::int[], $2::int[], $3::int[], $4::text[], $5::jsonb[])
			ON CONFLICT (lon_e5, lat_e5, dist, transp) DO UPDATE
				SET ring = EXCLUDED.ring, created_at = now(), last_used_at = now()`,
			lons, lats, dists, transps, rings); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM isochrone_cache WHERE created_at <= now() - make_interval(secs => $1)`,
		maxAge.Seconds()); err != nil {
		return err
	}
	if maxEntries > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM isochrone_cache WHERE ctid IN (
			SELECT ctid FROM isochrone_cache ORDER BY last_used_at DESC, created_at DESC OFFSET $1)`, maxEntries); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// QuartierProximite implémente Store.QuartierProximite. Les distances sont
// mesurées depuis ST_PointOnSurface du contour (point intérieur) ; pour
// chaque critère, les 5 plus proches voisins planaires (index GiST) sont
// départagés par leur distance géodésique.
func (s *PgStore) QuartierProximite(ctx context.Context, iris string, criteres []models.CritereNature) (*models.QuartierProximite, error) {
	const qInfo = `SELECT q.iris, q.nom_iris, q.code_commune, c.nom, ST_X(pt.g), ST_Y(pt.g),
			pi.prix, pi.ventes, pc.prix, pc.ventes,
			s.annee, s.score, s.nb_indicateurs,
			f.year, f.taux_global_tfb, f.taux_plein_teom
		FROM quartier q
		JOIN commune c ON c.code_commune = q.code_commune
		CROSS JOIN LATERAL (SELECT ST_PointOnSurface(q.contour) AS g) pt
		CROSS JOIN LATERAL (
			SELECT round(` + prixMedian + `)::integer AS prix, count(*)::integer AS ventes
			FROM immobilier i WHERE i.iris = q.iris
		) pi
		CROSS JOIN LATERAL (
			SELECT round(` + prixMedian + `)::integer AS prix, count(*)::integer AS ventes
			FROM quartier q2 JOIN immobilier i ON i.iris = q2.iris
			WHERE q2.code_commune = q.code_commune
		) pc
		LEFT JOIN securite_score s ON s.code_commune = q.code_commune
			AND s.annee = (SELECT max(annee) FROM securite_score)
		LEFT JOIN fiscalite f ON f.code_commune = q.code_commune
			AND f.year = (SELECT max(year) FROM fiscalite)
		WHERE q.iris = $1`

	var (
		out                 models.QuartierProximite
		lon, lat            float64
		prixI, prixC        *int
		ventesI, ventesC    int
		sAnnee, sScore, sNb *int
		fYear               *int
		tfb, teom           *float64
	)
	err := s.pool.QueryRow(ctx, qInfo, iris).Scan(&out.Iris, &out.Nom, &out.CodeCommune, &out.Commune, &lon, &lat,
		&prixI, &ventesI, &prixC, &ventesC, &sAnnee, &sScore, &sNb, &fYear, &tfb, &teom)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if prixI != nil {
		out.Prix = &models.PrixM2{PrixM2: *prixI, Ventes: ventesI}
	}
	if prixC != nil {
		out.PrixCommune = &models.PrixM2{PrixM2: *prixC, Ventes: ventesC}
	}
	if sAnnee != nil && sScore != nil && sNb != nil {
		out.Securite = &models.SecuriteCommune{Annee: *sAnnee, Score: *sScore, NbIndicateurs: *sNb}
	}
	if fYear != nil && tfb != nil {
		f := models.Fiscalite{Annee: *fYear, TauxTFB: *tfb, TauxTEOM: teom, Taux: *tfb}
		if teom != nil {
			f.Taux += *teom
		}
		out.Fiscalite = &f
	}

	out.Lon, out.Lat = lon, lat
	out.Proches = []models.Proche{}
	if len(criteres) == 0 {
		return &out, nil
	}
	q := `WITH ` + criteresCTE(3) + `,
		p AS (SELECT ST_MakePoint($1, $2) AS g)
		SELECT c.nature, n.dist, n.uai, n.nom, n.commune, n.secteur, n.taux, n.lon, n.lat
		FROM c CROSS JOIN p
		LEFT JOIN LATERAL (
			SELECT x.* FROM (
				SELECT e."UAI" AS uai, e.nom, e.commune, e.secteur, t.taux,
					ST_X(e.position) AS lon, ST_Y(e.position) AS lat,
					ST_Distance(ST_SetSRID(e.position, 4326)::geography, ST_SetSRID(p.g, 4326)::geography) AS dist
				FROM etablissement e
				LEFT JOIN LATERAL (SELECT et.taux FROM etablissement_taux et WHERE et.uai = e."UAI") t ON TRUE
				WHERE ` + critereMatch + `
				ORDER BY e.position <-> p.g
				LIMIT 5
			) x ORDER BY x.dist LIMIT 1
		) n ON TRUE
		ORDER BY c.k`
	rows, err := s.pool.Query(ctx, q, append([]any{lon, lat}, criteresArgs(criteres)...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p models.Proche
		if err := rows.Scan(&p.Nature, &p.DistanceM, &p.UAI, &p.Nom, &p.Commune, &p.Secteur, &p.Taux, &p.Lon, &p.Lat); err != nil {
			return nil, err
		}
		out.Proches = append(out.Proches, p)
	}
	return &out, rows.Err()
}
