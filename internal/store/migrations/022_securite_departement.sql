-- Score de sécurité relatif au département, et classement des départements.
--
-- Table `departement` : référentiel statique des 101 départements (code INSEE,
-- nom), rejouable (ON CONFLICT … DO UPDATE).
--
-- Vue matérialisée `securite_departement`, construite sur securite_score, une
-- ligne par (commune, année) :
--   - departement : code du département, déduit du code commune (3 caractères
--     outre-mer, 2 sinon, dont 2A / 2B) ;
--   - score_departement : rang centile du score de la commune parmi les
--     communes de son département (même règle des ex aequo que le score
--     national : (percent_rank + cume_dist) / 2), sur 100 ; « plus sûre que N %
--     des communes du département ». Une commune seule dans son département
--     (Paris) vaut 50 ;
--   - dep_score_moyen : moyenne des scores nationaux des communes du
--     département ;
--   - dep_rang : rang du département parmi ceux de l'année, par score moyen
--     décroissant (1 = le plus sûr) ; nb_departements : nombre de départements
--     classés.
--
-- La vue doit être rafraîchie APRÈS securite_score (REFRESH MATERIALIZED VIEW
-- securite_score; REFRESH MATERIALIZED VIEW securite_departement; fait par
-- scripts/import_securite_parquet.py). Idempotente.
CREATE TABLE IF NOT EXISTS departement (
    code TEXT PRIMARY KEY,
    nom  TEXT NOT NULL
);

INSERT INTO departement (code, nom) VALUES
    ('01', 'Ain'), ('02', 'Aisne'), ('03', 'Allier'), ('04', 'Alpes-de-Haute-Provence'),
    ('05', 'Hautes-Alpes'), ('06', 'Alpes-Maritimes'), ('07', 'Ardèche'), ('08', 'Ardennes'),
    ('09', 'Ariège'), ('10', 'Aube'), ('11', 'Aude'), ('12', 'Aveyron'),
    ('13', 'Bouches-du-Rhône'), ('14', 'Calvados'), ('15', 'Cantal'), ('16', 'Charente'),
    ('17', 'Charente-Maritime'), ('18', 'Cher'), ('19', 'Corrèze'), ('2A', 'Corse-du-Sud'),
    ('2B', 'Haute-Corse'), ('21', 'Côte-d''Or'), ('22', 'Côtes-d''Armor'), ('23', 'Creuse'),
    ('24', 'Dordogne'), ('25', 'Doubs'), ('26', 'Drôme'), ('27', 'Eure'),
    ('28', 'Eure-et-Loir'), ('29', 'Finistère'), ('30', 'Gard'), ('31', 'Haute-Garonne'),
    ('32', 'Gers'), ('33', 'Gironde'), ('34', 'Hérault'), ('35', 'Ille-et-Vilaine'),
    ('36', 'Indre'), ('37', 'Indre-et-Loire'), ('38', 'Isère'), ('39', 'Jura'),
    ('40', 'Landes'), ('41', 'Loir-et-Cher'), ('42', 'Loire'), ('43', 'Haute-Loire'),
    ('44', 'Loire-Atlantique'), ('45', 'Loiret'), ('46', 'Lot'), ('47', 'Lot-et-Garonne'),
    ('48', 'Lozère'), ('49', 'Maine-et-Loire'), ('50', 'Manche'), ('51', 'Marne'),
    ('52', 'Haute-Marne'), ('53', 'Mayenne'), ('54', 'Meurthe-et-Moselle'), ('55', 'Meuse'),
    ('56', 'Morbihan'), ('57', 'Moselle'), ('58', 'Nièvre'), ('59', 'Nord'),
    ('60', 'Oise'), ('61', 'Orne'), ('62', 'Pas-de-Calais'), ('63', 'Puy-de-Dôme'),
    ('64', 'Pyrénées-Atlantiques'), ('65', 'Hautes-Pyrénées'), ('66', 'Pyrénées-Orientales'), ('67', 'Bas-Rhin'),
    ('68', 'Haut-Rhin'), ('69', 'Rhône'), ('70', 'Haute-Saône'), ('71', 'Saône-et-Loire'),
    ('72', 'Sarthe'), ('73', 'Savoie'), ('74', 'Haute-Savoie'), ('75', 'Paris'),
    ('76', 'Seine-Maritime'), ('77', 'Seine-et-Marne'), ('78', 'Yvelines'), ('79', 'Deux-Sèvres'),
    ('80', 'Somme'), ('81', 'Tarn'), ('82', 'Tarn-et-Garonne'), ('83', 'Var'),
    ('84', 'Vaucluse'), ('85', 'Vendée'), ('86', 'Vienne'), ('87', 'Haute-Vienne'),
    ('88', 'Vosges'), ('89', 'Yonne'), ('90', 'Territoire de Belfort'), ('91', 'Essonne'),
    ('92', 'Hauts-de-Seine'), ('93', 'Seine-Saint-Denis'), ('94', 'Val-de-Marne'), ('95', 'Val-d''Oise'),
    ('971', 'Guadeloupe'), ('972', 'Martinique'), ('973', 'Guyane'), ('974', 'La Réunion'),
    ('976', 'Mayotte')
ON CONFLICT (code) DO UPDATE SET nom = EXCLUDED.nom;

CREATE MATERIALIZED VIEW IF NOT EXISTS securite_departement AS
WITH s AS (
    SELECT code_commune, annee, score,
           CASE WHEN code_commune LIKE '97%' THEN left(code_commune, 3)
                ELSE left(code_commune, 2) END AS departement
    FROM securite_score
),
dep AS (
    SELECT annee, departement, round(avg(score), 1)::double precision AS score_moyen
    FROM s
    GROUP BY annee, departement
),
dep_rang AS (
    SELECT annee, departement, score_moyen,
           rank() OVER (PARTITION BY annee ORDER BY score_moyen DESC)::integer AS rang,
           count(*) OVER (PARTITION BY annee)::integer AS nb_departements
    FROM dep
)
SELECT s.code_commune, s.annee, s.departement,
       round(100 * (percent_rank() OVER w + cume_dist() OVER w) / 2)::integer AS score_departement,
       d.score_moyen AS dep_score_moyen,
       d.rang AS dep_rang,
       d.nb_departements
FROM s
JOIN dep_rang d ON d.annee = s.annee AND d.departement = s.departement
WINDOW w AS (PARTITION BY s.annee, s.departement ORDER BY s.score)
;

CREATE UNIQUE INDEX IF NOT EXISTS securite_departement_pk ON securite_departement (code_commune, annee);
