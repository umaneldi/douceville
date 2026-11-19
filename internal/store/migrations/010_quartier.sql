-- Quartiers IRIS (IGN « contours IRIS », WGS84). Table créée vide ;
-- chargée par scripts/import_contours_iris.py.
CREATE TABLE IF NOT EXISTS quartier (
    -- Code IRIS complet (9 caractères : code INSEE + n° IRIS, propriété
    -- `code_iris` de la source) ; PRIMARY KEY : unique et non NULL.
    iris         TEXT NOT NULL PRIMARY KEY,
    -- Code INSEE de la commune (`code_insee`) ; les arrondissements de
    -- Paris/Marseille/Lyon sont ramenés à la commune parente.
    code_commune TEXT NOT NULL REFERENCES commune (code_commune),
    nom_iris     TEXT NOT NULL,
    -- H habitat, A activité, D divers, Z commune non découpée.
    type_iris    TEXT NOT NULL,
    contour      geometry(MultiPolygon, 4326) NOT NULL
);
CREATE INDEX IF NOT EXISTS quartier_code_commune_idx ON quartier (code_commune);
CREATE INDEX IF NOT EXISTS quartier_contour_gist ON quartier USING GIST (contour);
