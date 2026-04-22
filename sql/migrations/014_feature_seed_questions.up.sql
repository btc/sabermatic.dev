UPDATE questions SET is_featured = true, featured_order = 1 WHERE source = 'seed' AND user_id IS NULL AND title = 'Video Streaming';
UPDATE questions SET is_featured = true, featured_order = 2 WHERE source = 'seed' AND user_id IS NULL AND title = 'News Feed';
UPDATE questions SET is_featured = true, featured_order = 3 WHERE source = 'seed' AND user_id IS NULL AND title = 'Ride Sharing';
UPDATE questions SET is_featured = true, featured_order = 4 WHERE source = 'seed' AND user_id IS NULL AND title = 'Chat System';
UPDATE questions SET is_featured = true, featured_order = 5 WHERE source = 'seed' AND user_id IS NULL AND title = 'Search Autocomplete';
UPDATE questions SET is_featured = true, featured_order = 6 WHERE source = 'seed' AND user_id IS NULL AND title = 'Social Graph';
