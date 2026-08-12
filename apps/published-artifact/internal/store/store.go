package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/eli0shin/proxmox-config/apps/published-artifact/internal/archive"
	artifactnames "github.com/eli0shin/proxmox-config/apps/published-artifact/internal/names"
	"github.com/eli0shin/proxmox-config/apps/published-artifact/internal/store/catalogdb"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

var ErrPublicationSuperseded = errors.New("Publication Attempt was superseded")

type Store struct {
	db           *sql.DB
	databaseFile string
	versionsRoot string
}

type Artifact struct {
	Name             string `json:"name"`
	CurrentVersionID string `json:"current_version_id"`
	PublishedAt      string `json:"published_at"`
}

type Publication struct {
	Artifact
	Stats archive.Stats `json:"-"`
}

type Version struct {
	ID          string `json:"id"`
	PublishedAt string `json:"published_at"`
	Current     bool   `json:"current"`
}

type PublicationAttempt struct {
	Token      string
	Generation int64
}

type immediateTransaction struct {
	connection *sql.Conn
	committed  bool
}

func beginImmediate(ctx context.Context, database *sql.DB) (*immediateTransaction, error) {
	connection, err := database.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return &immediateTransaction{connection: connection}, nil
}

func (tx *immediateTransaction) Commit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := tx.connection.ExecContext(context.WithoutCancel(ctx), "COMMIT"); err != nil {
		return err
	}
	tx.committed = true
	return nil
}

func (tx *immediateTransaction) Close() {
	if !tx.committed {
		_, _ = tx.connection.ExecContext(context.Background(), "ROLLBACK")
	}
	_ = tx.connection.Close()
}

func Open(ctx context.Context, databasePath, versionsRoot string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o750); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	if err := os.MkdirAll(versionsRoot, 0o750); err != nil {
		return nil, fmt.Errorf("create versions directory: %w", err)
	}

	dsn := (&url.URL{Scheme: "file", Path: databasePath}).String() + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open catalog: %w", err)
	}
	catalog := &Store{db: db, databaseFile: databasePath, versionsRoot: versionsRoot}
	if err := catalog.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := catalog.Ready(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return catalog, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) BeginPublicationAttempt(ctx context.Context, name string) (PublicationAttempt, error) {
	token, err := uuid.NewV7()
	if err != nil {
		return PublicationAttempt{}, fmt.Errorf("generate Publication Attempt token: %w", err)
	}
	tx, err := beginImmediate(ctx, s.db)
	if err != nil {
		return PublicationAttempt{}, fmt.Errorf("begin Publication Attempt transaction: %w", err)
	}
	defer tx.Close()
	queries := catalogdb.New(tx.connection)
	generation, err := queries.NextPublicationGeneration(ctx)
	if err == nil {
		err = queries.RegisterPublicationAttempt(ctx, catalogdb.RegisterPublicationAttemptParams{
			ArtifactName: name,
			Token:        token.String(),
			Generation:   generation,
		})
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return PublicationAttempt{}, fmt.Errorf("register Publication Attempt: %w", err)
	}
	return PublicationAttempt{Token: token.String(), Generation: generation}, nil
}

func (s *Store) PublicationAttemptCurrent(ctx context.Context, name, token string) (bool, error) {
	current, err := catalogdb.New(s.db).GetPublicationAttemptToken(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return current == token, nil
}

func (s *Store) PublicationAttemptExists(ctx context.Context, name string) (bool, error) {
	_, err := catalogdb.New(s.db).GetPublicationAttemptToken(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) CompletePublicationAttempt(ctx context.Context, name, token string) error {
	return catalogdb.New(s.db).DeletePublicationAttemptToken(ctx, catalogdb.DeletePublicationAttemptTokenParams{
		ArtifactName: name,
		Token:        token,
	})
}

func (s *Store) Publish(ctx context.Context, name, attemptToken string, attemptGeneration int64, body io.Reader) (Publication, error) {
	versionID, err := uuid.NewV7()
	if err != nil {
		return Publication{}, fmt.Errorf("generate version ID: %w", err)
	}
	versionPath := filepath.Join(s.versionsRoot, versionID.String())
	_, filesystemSpan := otel.Tracer("published-artifact/store").Start(ctx, "filesystem.create_version",
		trace.WithAttributes(attribute.String("artifact.version.id", versionID.String())))
	if err := os.Mkdir(versionPath, 0o750); err != nil {
		filesystemSpan.RecordError(err)
		filesystemSpan.SetStatus(codes.Error, err.Error())
		filesystemSpan.End()
		return Publication{}, fmt.Errorf("create version directory: %w", err)
	}
	filesystemSpan.End()
	keepDirectory := false
	defer func() {
		if !keepDirectory {
			_ = os.RemoveAll(versionPath)
		}
	}()

	stats, err := archive.Extract(ctx, body, versionPath)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return Publication{}, contextErr
		}
		return Publication{}, err
	}
	if err := ctx.Err(); err != nil {
		return Publication{}, err
	}
	publishedAt := time.Now().UTC().Format(time.RFC3339Nano)

	ctx, catalogSpan := otel.Tracer("published-artifact/store").Start(ctx, "catalog.publish",
		trace.WithAttributes(
			attribute.String("db.system.name", "sqlite"),
			attribute.String("artifact.name", name),
			attribute.String("artifact.version.id", versionID.String()),
		))
	name, err = s.commitPublication(ctx, name, attemptToken, attemptGeneration, versionID.String(), publishedAt)
	if err != nil {
		recordSpanError(catalogSpan, err)
		catalogSpan.End()
		if !errors.Is(err, ErrPublicationSuperseded) && !errors.Is(err, context.Canceled) {
			keepDirectory = true
		}
		return Publication{}, fmt.Errorf("commit publication: %w", err)
	}
	catalogSpan.SetAttributes(attribute.String("artifact.name", name))
	catalogSpan.End()
	keepDirectory = true
	return Publication{Artifact: Artifact{Name: name, CurrentVersionID: versionID.String(), PublishedAt: publishedAt}, Stats: stats}, nil
}

func (s *Store) commitPublication(ctx context.Context, name, attemptToken string, attemptGeneration int64, versionID, publishedAt string) (string, error) {
	if name == "" {
		return s.commitGeneratedPublication(ctx, versionID, publishedAt)
	}
	tx, err := beginImmediate(ctx, s.db)
	if err != nil {
		return "", err
	}
	defer tx.Close()
	queries := catalogdb.New(tx.connection)
	currentToken, err := queries.GetPublicationAttemptToken(ctx, name)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && currentToken != attemptToken) {
		err = ErrPublicationSuperseded
	}
	if err == nil {
		err = queries.InsertVersion(ctx, catalogdb.InsertVersionParams{
			ID:           versionID,
			ArtifactName: name,
			PublishedAt:  publishedAt,
			Sequence:     attemptGeneration,
		})
	}
	if err == nil {
		err = queries.UpsertArtifact(ctx, catalogdb.UpsertArtifactParams{Name: name, CurrentVersionID: versionID})
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return name, err
}

func (s *Store) commitGeneratedPublication(ctx context.Context, versionID, publishedAt string) (name string, err error) {
	tx, err := beginImmediate(ctx, s.db)
	if err != nil {
		return "", err
	}
	defer tx.Close()
	queries := catalogdb.New(tx.connection)
	generation, err := queries.NextPublicationGeneration(ctx)
	if err != nil {
		return "", err
	}
	existing, err := queries.ListReservedArtifactNames(ctx)
	if err != nil {
		return "", err
	}
	taken := make(map[string]bool, len(existing))
	for _, existingName := range existing {
		taken[existingName] = true
	}
	name = artifactnames.Generate(taken)
	if err = queries.InsertVersion(ctx, catalogdb.InsertVersionParams{
		ID: versionID, ArtifactName: name, PublishedAt: publishedAt, Sequence: generation,
	}); err != nil {
		return "", err
	}
	if err = queries.InsertArtifact(ctx, catalogdb.InsertArtifactParams{Name: name, CurrentVersionID: versionID}); err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return name, nil
}

func (s *Store) Get(ctx context.Context, name string) (Artifact, error) {
	row, err := catalogdb.New(s.db).GetArtifact(ctx, strings.ToLower(name))
	if err != nil {
		return Artifact{}, err
	}
	return Artifact{Name: row.Name, CurrentVersionID: row.CurrentVersionID, PublishedAt: row.PublishedAt}, nil
}

func (s *Store) ListArtifacts(ctx context.Context) ([]Artifact, error) {
	rows, err := catalogdb.New(s.db).ListArtifacts(ctx)
	if err != nil {
		return nil, err
	}
	artifacts := make([]Artifact, 0, len(rows))
	for _, row := range rows {
		artifacts = append(artifacts, Artifact{Name: row.Name, CurrentVersionID: row.CurrentVersionID, PublishedAt: row.PublishedAt})
	}
	return artifacts, nil
}

func (s *Store) DeleteArtifact(ctx context.Context, name string) (err error) {
	name = strings.ToLower(name)
	requestCtx := ctx
	ctx, span := otel.Tracer("published-artifact/store").Start(ctx, "catalog.delete_artifact",
		trace.WithAttributes(attribute.String("db.system.name", "sqlite"), attribute.String("artifact.name", name)))
	catalogComplete := false
	defer func() {
		if !catalogComplete {
			if err != nil {
				recordSpanError(span, err)
			}
			span.End()
		}
	}()
	tx, err := beginImmediate(ctx, s.db)
	if err != nil {
		return fmt.Errorf("begin Artifact deletion: %w", err)
	}
	defer tx.Close()
	queries := catalogdb.New(tx.connection)
	if err = queries.DeletePublicationAttempt(ctx, name); err != nil {
		return err
	}
	if _, err = queries.GetArtifact(ctx, name); errors.Is(err, sql.ErrNoRows) {
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return commitErr
		}
		return sql.ErrNoRows
	} else if err != nil {
		return err
	}
	versionIDs, err := queries.ListVersionIDs(ctx, name)
	if err == nil {
		err = queries.DeleteArtifactRecord(ctx, name)
	}
	if err == nil {
		err = queries.DeleteVersionsForArtifact(ctx, name)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	span.End()
	catalogComplete = true
	_, filesystemSpan := otel.Tracer("published-artifact/store").Start(requestCtx, "filesystem.delete_artifact_versions",
		trace.WithAttributes(attribute.String("artifact.name", name)))
	defer func() {
		if err != nil {
			recordSpanError(filesystemSpan, err)
		}
		filesystemSpan.End()
	}()
	for _, versionID := range versionIDs {
		if err := os.RemoveAll(s.VersionPath(versionID)); err != nil {
			return fmt.Errorf("remove Version directory: %w", err)
		}
	}
	return nil
}

func (s *Store) ListVersions(ctx context.Context, name string) ([]Version, error) {
	name = strings.ToLower(name)
	artifact, err := catalogdb.New(s.db).GetArtifact(ctx, name)
	if err != nil {
		return nil, err
	}
	rows, err := catalogdb.New(s.db).ListVersions(ctx, name)
	if err != nil {
		return nil, err
	}
	versions := make([]Version, 0, len(rows))
	for _, row := range rows {
		versions = append(versions, Version{ID: row.ID, PublishedAt: row.PublishedAt, Current: row.ID == artifact.CurrentVersionID})
	}
	return versions, nil
}

func (s *Store) DeleteVersion(ctx context.Context, name, versionID string) (err error) {
	name = strings.ToLower(name)
	requestCtx := ctx
	ctx, span := otel.Tracer("published-artifact/store").Start(ctx, "catalog.delete_version",
		trace.WithAttributes(
			attribute.String("db.system.name", "sqlite"),
			attribute.String("artifact.name", name),
			attribute.String("artifact.version.id", versionID),
		))
	catalogComplete := false
	defer func() {
		if !catalogComplete {
			if err != nil {
				recordSpanError(span, err)
			}
			span.End()
		}
	}()
	tx, err := beginImmediate(ctx, s.db)
	if err != nil {
		return fmt.Errorf("begin Version deletion: %w", err)
	}
	defer tx.Close()
	queries := catalogdb.New(tx.connection)
	artifact, err := queries.GetArtifact(ctx, name)
	if err == nil {
		_, err = queries.GetVersion(ctx, catalogdb.GetVersionParams{ArtifactName: name, ID: versionID})
	}
	if err == nil && artifact.CurrentVersionID == versionID {
		earlier, earlierErr := queries.GetEarlierVersion(ctx, catalogdb.GetEarlierVersionParams{
			ArtifactName: name, ArtifactName_2: name, ID: versionID,
		})
		switch {
		case earlierErr == nil:
			err = queries.SetCurrentVersion(ctx, catalogdb.SetCurrentVersionParams{CurrentVersionID: earlier.ID, Name: name})
		case errors.Is(earlierErr, sql.ErrNoRows):
			err = queries.DeleteArtifactRecord(ctx, name)
		default:
			err = earlierErr
		}
	}
	if err == nil {
		err = queries.DeleteVersionRecord(ctx, catalogdb.DeleteVersionRecordParams{ArtifactName: name, ID: versionID})
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	span.End()
	catalogComplete = true
	_, filesystemSpan := otel.Tracer("published-artifact/store").Start(requestCtx, "filesystem.delete_version",
		trace.WithAttributes(
			attribute.String("artifact.name", name),
			attribute.String("artifact.version.id", versionID),
		))
	defer func() {
		if err != nil {
			recordSpanError(filesystemSpan, err)
		}
		filesystemSpan.End()
	}()
	if err := os.RemoveAll(s.VersionPath(versionID)); err != nil {
		return fmt.Errorf("remove Version directory: %w", err)
	}
	return nil
}

func (s *Store) VersionPath(versionID string) string {
	return filepath.Join(s.versionsRoot, versionID)
}

func (s *Store) Ready(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping catalog: %w", err)
	}
	for _, path := range []string{filepath.Dir(s.databaseFile), s.versionsRoot} {
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("stat storage root: %w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("storage root %q is not a directory", path)
		}
		if err := unix.Access(path, unix.W_OK|unix.X_OK); err != nil {
			return fmt.Errorf("storage root %q is not writable: %w", path, err)
		}
	}
	if err := unix.Access(s.databaseFile, unix.W_OK); err != nil {
		return fmt.Errorf("catalog is not writable: %w", err)
	}
	return nil
}

func (s *Store) migrate(ctx context.Context) (err error) {
	connection, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open migration connection: %w", err)
	}
	defer connection.Close()
	if _, err = connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("lock migration catalog: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	if _, err = connection.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY)`); err != nil {
		return fmt.Errorf("create migration catalog: %w", err)
	}
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		var applied int
		if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, entry.Name()).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %s: %w", entry.Name(), err)
		}
		if applied != 0 {
			continue
		}
		contents, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		if _, err = connection.ExecContext(ctx, string(contents)); err == nil {
			_, err = connection.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (?)`, entry.Name())
		}
		if err != nil {
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}
	}
	if _, err = connection.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	committed = true
	return nil
}

func recordSpanError(span trace.Span, err error) {
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }
