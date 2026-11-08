-- Apply in a dedicated schema with an explicit search_path. Constructors do not
-- run this migration. Numeric Unix nanoseconds preserve Go query boundaries,
-- including values finer than PostgreSQL timestamptz's microsecond precision.
CREATE TABLE ratelimit_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    key text NOT NULL,
    at_ns numeric(30,0) NOT NULL
);
CREATE INDEX ratelimit_events_key_time ON ratelimit_events (key, at_ns);
CREATE INDEX ratelimit_events_time ON ratelimit_events (at_ns);

CREATE TABLE ratelimit_buckets (
    key text NOT NULL,
    resolution_ns bigint NOT NULL CHECK (resolution_ns > 0),
    start_ns numeric(30,0) NOT NULL,
    count bigint NOT NULL CHECK (count > 0),
    PRIMARY KEY (key, resolution_ns, start_ns)
);
CREATE INDEX ratelimit_buckets_resolution_time ON ratelimit_buckets (resolution_ns, start_ns);
