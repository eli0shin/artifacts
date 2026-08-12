CREATE TABLE versions (
    id TEXT PRIMARY KEY,
    artifact_name TEXT NOT NULL,
    published_at TEXT NOT NULL,
    sequence INTEGER NOT NULL UNIQUE,
    UNIQUE (artifact_name, id)
);

CREATE TABLE artifacts (
    name TEXT PRIMARY KEY,
    current_version_id TEXT NOT NULL,
    FOREIGN KEY (name, current_version_id)
        REFERENCES versions (artifact_name, id)
);

CREATE INDEX versions_by_artifact_and_id
    ON versions (artifact_name, id DESC);

CREATE INDEX versions_by_artifact_and_sequence
    ON versions (artifact_name, sequence DESC);

CREATE TABLE publication_generation (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    next_generation INTEGER NOT NULL
);

INSERT INTO publication_generation (id, next_generation) VALUES (1, 0);

CREATE TABLE publication_attempts (
    artifact_name TEXT PRIMARY KEY,
    token TEXT NOT NULL UNIQUE,
    generation INTEGER NOT NULL UNIQUE
);
