-- Écoles primaires : un établissement dont le nom contient « primaire »
-- (maternelle et élémentaire réunies) a la nature 'Primaire', qui satisfait
-- les critères de recherche maternelle et élémentaire. Même règle que
-- scripts/import_etablissements_parquet.py ; cette mise à jour reclasse les
-- établissements importés auparavant. Idempotente.
UPDATE etablissement
SET nature = 'Primaire'
WHERE nom ILIKE '%primaire%' AND nature IS DISTINCT FROM 'Primaire';
