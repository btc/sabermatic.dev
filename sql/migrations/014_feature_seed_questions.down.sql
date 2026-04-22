UPDATE questions SET is_featured = false, featured_order = NULL
WHERE source = 'seed' AND user_id IS NULL AND title IN (
    'Video Streaming',
    'News Feed',
    'Ride Sharing',
    'Chat System',
    'Search Autocomplete',
    'Social Graph'
);
