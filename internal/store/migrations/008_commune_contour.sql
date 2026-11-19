-- Contour géographique des communes (Etalab « contours administratifs »,
-- WGS84). Chargé par scripts/import_contours_communes.py ; NULL tant que
-- l'import n'a pas été fait.
ALTER TABLE commune ADD COLUMN IF NOT EXISTS contour geometry(MultiPolygon, 4326);
CREATE INDEX IF NOT EXISTS commune_contour_gist ON commune USING GIST (contour);
