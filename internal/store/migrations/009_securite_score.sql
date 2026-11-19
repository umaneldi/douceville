-- Score de sécurité par commune et par année (0 = moins sûr, 100 = plus sûr).
--
-- 1. Pour chaque (année, indicateur), rang centile du taux pour mille de la
--    commune parmi les communes ayant diffusé cet indicateur. Les ex aequo
--    (très nombreux à 0) reçoivent le rang moyen : (percent_rank + cume_dist) / 2.
-- 2. Indice composite = moyenne pondérée de ces rangs sur les seuls indicateurs
--    diffusés (les non diffusés, secret statistique, sont ignorés).
-- 3. Score = rang centile inversé de l'indice parmi les communes de l'année,
--    ramené sur 100 : un score de 70 signifie « plus sûre que 70 % des communes ».
-- Une commune sans aucun indicateur diffusé est absente de la vue (score inconnu).
--
-- La vue doit être rafraîchie après chaque import de `securite`
-- (REFRESH MATERIALIZED VIEW securite_score, fait par les scripts d'import).
-- Pour modifier le calcul ou les poids : nouvelle migration DROP puis CREATE.
CREATE MATERIALIZED VIEW IF NOT EXISTS securite_score AS
WITH poids (indicateur, poids) AS (
    VALUES
        ('Violences physiques hors cadre familial',        3.0),
        ('Violences physiques intrafamiliales',            3.0),
        ('Violences sexuelles',                            3.0),
        ('Vols avec armes',                                3.0),
        ('Vols violents sans arme',                        3.0),
        ('Cambriolages de logement',                       3.0),
        ('Destructions et dégradations volontaires',       2.0),
        ('Vols de véhicule',                               2.0),
        ('Vols dans les véhicules',                        2.0),
        ('Vols d''accessoires sur véhicules',              2.0),
        ('Vols sans violence contre des personnes',        2.0),
        ('Escroqueries et fraudes aux moyens de paiement', 1.0),
        ('Trafic de stupéfiants',                          1.0),
        ('Usage de stupéfiants',                           1.0),
        ('Usage de stupéfiants (AFD)',                     1.0)
),
rangs AS (
    SELECT s.code_commune, s.annee, s.indicateur,
           (percent_rank() OVER w + cume_dist() OVER w) / 2 AS rang
    FROM securite s
    WHERE s.est_diffuse AND s.taux_pour_mille IS NOT NULL
    WINDOW w AS (PARTITION BY s.annee, s.indicateur ORDER BY s.taux_pour_mille)
),
composite AS (
    SELECT r.code_commune, r.annee,
           sum(r.rang * COALESCE(p.poids, 1.0)) / sum(COALESCE(p.poids, 1.0)) AS indice,
           count(*)::integer AS nb_indicateurs
    FROM rangs r
    LEFT JOIN poids p ON p.indicateur = r.indicateur
    GROUP BY r.code_commune, r.annee
)
SELECT code_commune, annee,
       round(100 * (percent_rank() OVER w + cume_dist() OVER w) / 2)::integer AS score,
       nb_indicateurs
FROM composite
WINDOW w AS (PARTITION BY annee ORDER BY indice DESC)
;

CREATE UNIQUE INDEX IF NOT EXISTS securite_score_pk ON securite_score (code_commune, annee);
