-- Rattachement des établissements au référentiel des communes (code INSEE).
-- NULL si la commune est absente de `commune` (collectivités d'outre-mer).
ALTER TABLE etablissement ADD COLUMN IF NOT EXISTS code_commune TEXT;

DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'etablissement_code_commune_fkey') THEN
        ALTER TABLE etablissement
            ADD CONSTRAINT etablissement_code_commune_fkey
            FOREIGN KEY (code_commune) REFERENCES commune (code_commune);
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS etablissement_code_commune
    ON etablissement (code_commune);
