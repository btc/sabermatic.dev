"""Seed questions for system design drill practice.

18 questions covering a range of system design topics at medium and hard difficulty.
Each has a deliberately vague interview-style prompt, tags, and optional hints.
"""

import asyncio
import json

import asyncpg


SEED_QUESTIONS = [
    {
        "title": "URL Shortener",
        "prompt": "Design a URL shortening service.",
        "difficulty": "medium",
        "tags": ["read-heavy", "hashing", "storage", "web"],
        "hints": {
            "areas_to_explore": [
                "How do you generate short URLs? Hash collisions?",
                "Read vs write ratio and caching strategy",
                "Analytics and click tracking",
                "Custom short URLs and expiration",
            ]
        },
    },
    {
        "title": "News Feed",
        "prompt": "Design a social media news feed.",
        "difficulty": "hard",
        "tags": ["read-heavy", "fanout", "caching", "ranking", "social"],
        "hints": {
            "areas_to_explore": [
                "Push vs pull model for feed generation",
                "Ranking and relevance algorithms",
                "Celebrity/influencer problem (fanout)",
                "Real-time updates vs polling",
            ]
        },
    },
    {
        "title": "Chat System",
        "prompt": "Design a real-time chat application.",
        "difficulty": "medium",
        "tags": ["real-time", "websocket", "messaging", "presence"],
        "hints": {
            "areas_to_explore": [
                "1:1 vs group chat differences",
                "Online presence and typing indicators",
                "Message delivery guarantees",
                "Message storage and history",
            ]
        },
    },
    {
        "title": "Rate Limiter",
        "prompt": "Design a distributed rate limiting service.",
        "difficulty": "medium",
        "tags": ["distributed", "algorithms", "infrastructure"],
        "hints": {
            "areas_to_explore": [
                "Sliding window vs fixed window vs token bucket",
                "Distributed rate limiting across multiple nodes",
                "Race conditions in counter updates",
                "What happens when the limiter itself becomes a bottleneck",
            ]
        },
    },
    {
        "title": "Video Streaming",
        "prompt": "Design a video streaming platform like YouTube.",
        "difficulty": "hard",
        "tags": ["storage", "streaming", "transcoding", "cdn", "write-heavy"],
        "hints": {
            "areas_to_explore": [
                "Video upload and transcoding pipeline",
                "Adaptive bitrate streaming",
                "CDN and edge caching for video delivery",
                "Recommendation engine considerations",
            ]
        },
    },
    {
        "title": "Web Crawler",
        "prompt": "Design a web crawler for a search engine.",
        "difficulty": "hard",
        "tags": ["distributed", "batch", "scheduling", "storage"],
        "hints": {
            "areas_to_explore": [
                "Politeness and robots.txt compliance",
                "URL frontier and prioritization",
                "Duplicate detection",
                "Distributed coordination across crawlers",
            ]
        },
    },
    {
        "title": "Autocomplete",
        "prompt": "Design a search autocomplete system.",
        "difficulty": "medium",
        "tags": ["read-heavy", "trie", "caching", "real-time"],
        "hints": {
            "areas_to_explore": [
                "Data structure for prefix matching (trie vs other)",
                "Ranking suggestions by popularity",
                "Updating suggestions with fresh data",
                "Latency requirements and caching",
            ]
        },
    },
    {
        "title": "File Sync",
        "prompt": "Design a file synchronization service like Dropbox.",
        "difficulty": "hard",
        "tags": ["storage", "sync", "distributed", "conflict-resolution"],
        "hints": {
            "areas_to_explore": [
                "Chunking and deduplication",
                "Conflict resolution strategies",
                "Sync protocol and change detection",
                "Bandwidth optimization and compression",
            ]
        },
    },
    {
        "title": "Notification System",
        "prompt": "Design a notification service that supports push, email, and SMS.",
        "difficulty": "medium",
        "tags": ["messaging", "distributed", "multi-channel"],
        "hints": {
            "areas_to_explore": [
                "Priority and rate limiting per user",
                "Template management and personalization",
                "Delivery guarantees across channels",
                "User preference management and opt-out",
            ]
        },
    },
    {
        "title": "Key-Value Store",
        "prompt": "Design a distributed key-value store.",
        "difficulty": "hard",
        "tags": ["distributed", "storage", "consistency", "infrastructure"],
        "hints": {
            "areas_to_explore": [
                "Consistency model (strong vs eventual)",
                "Partitioning and replication strategy",
                "Failure detection and recovery",
                "Read/write path and conflict resolution",
            ]
        },
    },
    {
        "title": "Ticket Booking",
        "prompt": "Design an online ticket booking system for events.",
        "difficulty": "medium",
        "tags": ["consistency", "concurrency", "booking", "write-heavy"],
        "hints": {
            "areas_to_explore": [
                "Seat reservation and hold mechanism",
                "Handling concurrent booking attempts",
                "Payment integration and failure handling",
                "Waitlist and cancellation flow",
            ]
        },
    },
    {
        "title": "Search Engine",
        "prompt": "Design a search engine.",
        "difficulty": "hard",
        "tags": ["distributed", "indexing", "ranking", "read-heavy"],
        "hints": {
            "areas_to_explore": [
                "Inverted index construction and storage",
                "Query parsing and relevance ranking",
                "Distributed index across machines",
                "Freshness vs completeness tradeoffs",
            ]
        },
    },
    {
        "title": "Metrics Collection",
        "prompt": "Design a metrics collection and monitoring system.",
        "difficulty": "medium",
        "tags": ["time-series", "write-heavy", "aggregation", "infrastructure"],
        "hints": {
            "areas_to_explore": [
                "Time-series data storage and compression",
                "Aggregation at different time granularities",
                "Alerting rules and threshold evaluation",
                "High write throughput ingestion",
            ]
        },
    },
    {
        "title": "Payment System",
        "prompt": "Design a payment processing system.",
        "difficulty": "hard",
        "tags": ["consistency", "reliability", "financial", "write-heavy"],
        "hints": {
            "areas_to_explore": [
                "Exactly-once processing and idempotency",
                "Reconciliation and audit trails",
                "Multi-currency and exchange rates",
                "Retry logic and failure handling",
            ]
        },
    },
    {
        "title": "Proximity Service",
        "prompt": "Design a service that finds nearby places or people.",
        "difficulty": "medium",
        "tags": ["geospatial", "indexing", "read-heavy"],
        "hints": {
            "areas_to_explore": [
                "Geospatial indexing (geohash, quadtree, R-tree)",
                "Dynamic vs static location data",
                "Radius search vs k-nearest neighbors",
                "Caching strategies for location queries",
            ]
        },
    },
    {
        "title": "Collaborative Editor",
        "prompt": "Design a real-time collaborative document editor.",
        "difficulty": "hard",
        "tags": ["real-time", "conflict-resolution", "sync", "distributed"],
        "hints": {
            "areas_to_explore": [
                "OT vs CRDT for conflict resolution",
                "Cursor and selection synchronization",
                "Operational history and undo/redo",
                "Offline editing and reconnection",
            ]
        },
    },
    {
        "title": "Ad Click Aggregation",
        "prompt": "Design a system that aggregates ad click data for real-time analytics.",
        "difficulty": "hard",
        "tags": ["streaming", "aggregation", "write-heavy", "analytics"],
        "hints": {
            "areas_to_explore": [
                "Stream processing vs batch processing",
                "Click deduplication and fraud detection",
                "Real-time aggregation windows",
                "Data reconciliation between real-time and batch",
            ]
        },
    },
    {
        "title": "CDN",
        "prompt": "Design a content delivery network.",
        "difficulty": "hard",
        "tags": ["distributed", "caching", "networking", "infrastructure"],
        "hints": {
            "areas_to_explore": [
                "Cache invalidation strategies",
                "Content routing and DNS-based load balancing",
                "Origin shielding and tiered caching",
                "Handling cache misses at edge nodes",
            ]
        },
    },
]


async def seed_questions(conn: asyncpg.Connection) -> int:
    """Insert seed questions if the questions table is empty.

    Returns the number of questions inserted.
    """
    count = await conn.fetchval("SELECT COUNT(*) FROM questions")
    if count > 0:
        print(f"  Questions table already has {count} rows, skipping seed.")
        return 0

    inserted = 0
    for q in SEED_QUESTIONS:
        await conn.execute(
            """
            INSERT INTO questions (title, prompt, difficulty, tags, hints, source)
            VALUES ($1, $2, $3::difficulty, $4, $5::jsonb, 'seed')
            """,
            q["title"],
            q["prompt"],
            q["difficulty"],
            q["tags"],
            json.dumps(q["hints"]) if q.get("hints") else None,
        )
        inserted += 1

    print(f"  Seeded {inserted} questions.")
    return inserted


async def main(database_url: str) -> None:
    conn = await asyncpg.connect(database_url)
    try:
        await seed_questions(conn)
    finally:
        await conn.close()


if __name__ == "__main__":
    import sys

    url = sys.argv[1] if len(sys.argv) > 1 else "postgresql://localhost:5432/drill"
    asyncio.run(main(url))
