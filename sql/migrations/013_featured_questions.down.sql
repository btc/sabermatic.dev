DROP INDEX IF EXISTS questions_featured_order_unique;
ALTER TABLE questions DROP CONSTRAINT IF EXISTS questions_featured_order_required;
ALTER TABLE questions DROP COLUMN IF EXISTS featured_order;
ALTER TABLE questions DROP COLUMN IF EXISTS is_featured;
