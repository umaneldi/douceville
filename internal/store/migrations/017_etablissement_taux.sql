-- Taux de réussite aux examens de chaque établissement, en % : Σadmis / Σprésents
-- sur tous ses résultats dont presents > 0 et admis sont renseignés. Même
-- définition que successRate() dans web/app.js (à garder synchronisées).
-- Un établissement sans tel résultat est absent de la vue (taux inconnu).
CREATE OR REPLACE VIEW etablissement_taux AS
SELECT etablissement_uai AS uai,
       100.0 * sum(admis)::float8 / sum(presents)::float8 AS taux
FROM resultat
WHERE presents > 0 AND admis IS NOT NULL AND etablissement_uai IS NOT NULL
GROUP BY etablissement_uai;
