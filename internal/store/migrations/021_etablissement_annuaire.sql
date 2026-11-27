-- Attributs de l'annuaire de l'éducation (data.education.gouv.fr) :
-- éducation prioritaire, services (restauration, internat, ULIS), offre
-- pédagogique (SEGPA, sections, apprentissage, post-bac, lycée des métiers) et
-- contact. Renseignés par scripts/import_etablissements_parquet.py : réimporter
-- les établissements pour remplir les colonnes sur une base existante.
-- NULL signifie « inconnu ». Idempotente.
ALTER TABLE etablissement ADD COLUMN IF NOT EXISTS education_prioritaire TEXT;
ALTER TABLE etablissement ADD COLUMN IF NOT EXISTS restauration BOOLEAN;
ALTER TABLE etablissement ADD COLUMN IF NOT EXISTS hebergement BOOLEAN;
ALTER TABLE etablissement ADD COLUMN IF NOT EXISTS ulis BOOLEAN;
ALTER TABLE etablissement ADD COLUMN IF NOT EXISTS segpa BOOLEAN;
ALTER TABLE etablissement ADD COLUMN IF NOT EXISTS apprentissage BOOLEAN;
ALTER TABLE etablissement ADD COLUMN IF NOT EXISTS post_bac BOOLEAN;
ALTER TABLE etablissement ADD COLUMN IF NOT EXISTS lycee_des_metiers BOOLEAN;
ALTER TABLE etablissement ADD COLUMN IF NOT EXISTS sections TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE etablissement ADD COLUMN IF NOT EXISTS telephone TEXT;
ALTER TABLE etablissement ADD COLUMN IF NOT EXISTS mail TEXT;
ALTER TABLE etablissement ADD COLUMN IF NOT EXISTS site_web TEXT;
ALTER TABLE etablissement ADD COLUMN IF NOT EXISTS fiche_onisep TEXT;
