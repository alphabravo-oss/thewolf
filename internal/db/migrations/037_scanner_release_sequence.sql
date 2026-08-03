-- Transactional, candidate-bound scanner release name reservations.
--
-- Counters and reservations are updated in the same transaction that writes
-- the immutable release inventory. A failed publication therefore consumes no
-- release sequence. Reservations remain as an audit-friendly uniqueness guard
-- after publication.

CREATE TABLE IF NOT EXISTS scanner_release_sequence_counters (
    period_key TEXT PRIMARY KEY,
    next_sequence INTEGER NOT NULL CHECK (next_sequence >= 1),
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS scanner_release_name_reservations (
    candidate_id TEXT PRIMARY KEY REFERENCES scanner_release_candidates(id) ON DELETE RESTRICT,
    period_key TEXT NOT NULL,
    sequence_number INTEGER NOT NULL CHECK (sequence_number >= 1),
    release_name TEXT NOT NULL UNIQUE,
    created_at TIMESTAMP NOT NULL,
    UNIQUE (period_key, sequence_number)
);
