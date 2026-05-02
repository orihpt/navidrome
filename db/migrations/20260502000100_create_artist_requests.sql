-- +goose Up
CREATE TABLE artist_request (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    normalized_name TEXT NOT NULL UNIQUE,
    status          TEXT NOT NULL DEFAULT 'wishlist' CHECK (status IN ('wishlist', 'available_soon')),
    created_by      TEXT NOT NULL REFERENCES user(id) ON DELETE CASCADE,
    created_at      DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at      DATETIME NOT NULL DEFAULT (datetime('now')),
    moved_at        DATETIME
);

CREATE TABLE artist_request_vote (
    artist_request_id TEXT NOT NULL REFERENCES artist_request(id) ON DELETE CASCADE,
    user_id           TEXT NOT NULL REFERENCES user(id) ON DELETE CASCADE,
    created_at        DATETIME NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (artist_request_id, user_id)
);

CREATE INDEX idx_artist_request_status ON artist_request(status);
CREATE INDEX idx_artist_request_vote_request ON artist_request_vote(artist_request_id);

-- +goose Down
DROP TABLE IF EXISTS artist_request_vote;
DROP TABLE IF EXISTS artist_request;
