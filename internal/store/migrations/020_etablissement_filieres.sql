-- Filières d'un lycée (Général, Technologique, Professionnel, Agricole),
-- plusieurs possibles ; tableau vide pour les autres établissements.
-- Renseignées par scripts/import_etablissements_parquet.py d'après le code
-- natureUAI et le ministère de tutelle : réimporter les établissements pour
-- remplir la colonne sur une base existante.
ALTER TABLE etablissement ADD COLUMN IF NOT EXISTS filieres TEXT[] NOT NULL DEFAULT '{}';
