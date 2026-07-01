DROP INDEX IF EXISTS idx_products_wp_cat_id;
ALTER TABLE products 
DROP COLUMN wp_cat_id,
DROP COLUMN price_coeff,
DROP COLUMN image_url,
DROP COLUMN eways_cat_id;