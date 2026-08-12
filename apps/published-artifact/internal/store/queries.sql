-- name: NextPublicationGeneration :one
UPDATE publication_generation
SET next_generation = next_generation + 1
WHERE id = 1
RETURNING next_generation;

-- name: RegisterPublicationAttempt :exec
INSERT INTO publication_attempts (artifact_name, token, generation)
VALUES (?, ?, ?)
ON CONFLICT (artifact_name) DO UPDATE
SET token = excluded.token,
    generation = excluded.generation;

-- name: GetPublicationAttemptToken :one
SELECT token FROM publication_attempts WHERE artifact_name = ?;

-- name: DeletePublicationAttempt :exec
DELETE FROM publication_attempts WHERE artifact_name = ?;

-- name: DeletePublicationAttemptToken :exec
DELETE FROM publication_attempts WHERE artifact_name = ? AND token = ?;

-- name: InsertVersion :exec
INSERT INTO versions (id, artifact_name, published_at, sequence)
VALUES (?, ?, ?, ?);

-- name: UpsertArtifact :exec
INSERT INTO artifacts (name, current_version_id)
VALUES (?, ?)
ON CONFLICT (name) DO UPDATE SET current_version_id = excluded.current_version_id;

-- name: InsertArtifact :exec
INSERT INTO artifacts (name, current_version_id)
VALUES (?, ?);

-- name: GetArtifact :one
SELECT a.name, a.current_version_id, v.published_at
FROM artifacts AS a
JOIN versions AS v
  ON v.artifact_name = a.name
 AND v.id = a.current_version_id
WHERE a.name = ?;

-- name: ListArtifacts :many
SELECT a.name, a.current_version_id, v.published_at
FROM artifacts AS a
JOIN versions AS v
  ON v.artifact_name = a.name
 AND v.id = a.current_version_id
ORDER BY a.name;

-- name: ListReservedArtifactNames :many
SELECT name FROM artifacts
UNION
SELECT artifact_name AS name FROM publication_attempts
ORDER BY name;

-- name: ListVersions :many
SELECT id, artifact_name, published_at, sequence
FROM versions
WHERE artifact_name = ?
ORDER BY sequence DESC;

-- name: GetVersion :one
SELECT id, artifact_name, published_at, sequence
FROM versions
WHERE artifact_name = ? AND id = ?;

-- name: GetEarlierVersion :one
SELECT v.id, v.artifact_name, v.published_at, v.sequence
FROM versions AS v
WHERE v.artifact_name = ?
  AND v.sequence < (
      SELECT current.sequence FROM versions AS current
      WHERE current.artifact_name = ? AND current.id = ?
  )
ORDER BY v.sequence DESC
LIMIT 1;

-- name: SetCurrentVersion :exec
UPDATE artifacts
SET current_version_id = ?
WHERE name = ?;

-- name: ListVersionIDs :many
SELECT id FROM versions WHERE artifact_name = ?;

-- name: DeleteVersionsForArtifact :exec
DELETE FROM versions WHERE artifact_name = ?;

-- name: DeleteArtifactRecord :exec
DELETE FROM artifacts WHERE name = ?;

-- name: DeleteVersionRecord :exec
DELETE FROM versions WHERE artifact_name = ? AND id = ?;
