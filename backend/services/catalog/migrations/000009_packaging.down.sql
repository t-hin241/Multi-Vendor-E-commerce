DROP TABLE IF EXISTS product_packaging;
DELETE FROM attributes WHERE code IN ('pkg_weight', 'pkg_length', 'pkg_width', 'pkg_height');
