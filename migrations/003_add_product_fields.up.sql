-- اضافه کردن ستون‌های جدید به جدول products
ALTER TABLE products 
ADD COLUMN wp_cat_id INT,
ADD COLUMN price_coeff DECIMAL(5,2) DEFAULT 1.0,
ADD COLUMN image_url TEXT,
ADD COLUMN eways_cat_id VARCHAR(50);

-- ایندکس برای wp_cat_id
CREATE INDEX idx_products_wp_cat_id ON products(wp_cat_id);