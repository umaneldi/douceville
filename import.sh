#! /bin/bash
set -e

# Communes first: etablissement.code_commune and securite.code_commune reference them.
uv run scripts/import_communes_csv.py --psql 'docker exec -i douceville_go-geotestdb-1 psql -U douceville -d douceville' .parquet/v_commune_depuis_1943.csv
uv run scripts/import_contours_communes.py --psql 'docker exec -i douceville_go-geotestdb-1 psql -U douceville -d douceville' .parquet/communes-50m.geojson.gz
uv run scripts/import_contours_iris.py --psql 'docker exec -i douceville_go-geotestdb-1 psql -U douceville -d douceville' --save .parquet/contours_iris.json.gz
uv run scripts/import_immobilier_dvf.py --psql 'docker exec -i douceville_go-geotestdb-1 psql -U douceville -d douceville' .parquet/demandes-de-valeurs-foncieres-geolocalisees.csv.gz
uv run scripts/import_etablissements_parquet.py --psql 'docker exec -i douceville_go-geotestdb-1 psql -U douceville -d douceville' .parquet/fr-en-adresse-et-geolocalisation-etablissements-premier-et-second-degre.parquet
uv run scripts/import_resultats_parquet.py --psql 'docker exec -i douceville_go-geotestdb-1 psql -U douceville -d douceville' --dir .parquet
uv run scripts/import_securite_parquet.py --psql 'docker exec -i douceville_go-geotestdb-1 psql -U douceville -d douceville' .parquet/donnee-comm-data.gouv-parquet-2025-geographie2026-produit-le2026-06-25.parquet --start-year 2024
