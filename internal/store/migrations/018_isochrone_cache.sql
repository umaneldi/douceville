-- Cache des isochrones OpenRouteService calculées pour les recherches :
-- une isochrone par position (arrondie au 1e-5 degré), temps (secondes) et
-- mode de transport. Les entrées plus anciennes que ISOCHRONE_CACHE_DAYS sont
-- recalculées puis purgées ; au-delà de ISOCHRONE_CACHE_MAX lignes, les
-- entrées les moins récemment utilisées sont supprimées.
CREATE TABLE IF NOT EXISTS isochrone_cache (
    lon_e5       INTEGER NOT NULL,
    lat_e5       INTEGER NOT NULL,
    dist         INTEGER NOT NULL,
    transp       TEXT NOT NULL,
    -- Anneau du polygone, tableau JSON de [lon, lat].
    ring         JSONB NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (lon_e5, lat_e5, dist, transp)
);
CREATE INDEX IF NOT EXISTS isochrone_cache_last_used_idx ON isochrone_cache (last_used_at);
CREATE INDEX IF NOT EXISTS isochrone_cache_created_idx ON isochrone_cache (created_at);
