ALTER TABLE questions
    ADD COLUMN is_featured BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN featured_order INTEGER;

ALTER TABLE questions
    ADD CONSTRAINT questions_featured_order_required
    CHECK (NOT is_featured OR featured_order IS NOT NULL);

CREATE UNIQUE INDEX questions_featured_order_unique
    ON questions (featured_order) WHERE is_featured;
