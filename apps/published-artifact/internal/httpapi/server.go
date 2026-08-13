package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eli0shin/artifacts/apps/published-artifact/internal/names"
	"github.com/eli0shin/artifacts/apps/published-artifact/internal/store"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type Server struct {
	store     *store.Store
	publicURL string
	draining  atomic.Bool
	attempts  attemptRegistry
	handler   http.Handler
}

type publicationAttempt struct {
	id         uint64
	generation int64
	interrupt  func()
}

type attemptRegistry struct {
	mu     sync.Mutex
	nextID uint64
	byName map[string]publicationAttempt
}

func (r *attemptRegistry) begin(parent context.Context, name string, generation int64, abort func()) (context.Context, func(), func(), bool) {
	ctx, cancel := context.WithCancel(parent)
	interrupt := func() {
		cancel()
		abort()
	}

	r.mu.Lock()
	if r.byName == nil {
		r.byName = make(map[string]publicationAttempt)
	}
	previous, hasPrevious := r.byName[name]
	if hasPrevious && previous.generation >= generation {
		r.mu.Unlock()
		interrupt()
		return ctx, func() {}, interrupt, false
	}
	r.nextID++
	id := r.nextID
	r.byName[name] = publicationAttempt{id: id, generation: generation, interrupt: interrupt}
	r.mu.Unlock()

	if hasPrevious {
		previous.interrupt()
	}
	finish := func() {
		cancel()
		r.mu.Lock()
		if current, ok := r.byName[name]; ok && current.id == id {
			delete(r.byName, name)
		}
		r.mu.Unlock()
	}
	return ctx, finish, interrupt, true
}

func (r *attemptRegistry) cancel(name string) {
	r.mu.Lock()
	attempt, ok := r.byName[name]
	if ok {
		delete(r.byName, name)
	}
	r.mu.Unlock()
	if ok {
		attempt.interrupt()
	}
}

type artifactResponse struct {
	Name             string `json:"name"`
	URL              string `json:"url"`
	CurrentVersionID string `json:"current_version_id"`
	PublishedAt      string `json:"published_at"`
}

func New(catalog *store.Store, publicURL string) *Server {
	s := &Server{store: catalog, publicURL: strings.TrimRight(publicURL, "/")}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleArtifactDirectory)
	mux.HandleFunc("GET /livez", s.handleLive)
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.HandleFunc("POST /api/v1/artifacts", s.handlePublish)
	mux.HandleFunc("GET /api/v1/artifacts", s.handleListArtifacts)
	mux.HandleFunc("GET /api/v1/artifacts/{name}", s.handleInspect)
	mux.HandleFunc("DELETE /api/v1/artifacts/{name}", s.handleDeleteArtifact)
	mux.HandleFunc("GET /api/v1/artifacts/{name}/versions", s.handleListVersions)
	mux.HandleFunc("DELETE /api/v1/artifacts/{name}/versions/{versionID}", s.handleDeleteVersion)
	mux.HandleFunc("GET /", s.handleStatic)
	s.handler = mux
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.draining.Load() && r.URL.Path != "/livez" && r.URL.Path != "/readyz" {
		http.Error(w, "server is draining", http.StatusServiceUnavailable)
		return
	}
	s.handler.ServeHTTP(w, r)
}

func (s *Server) Drain() {
	s.draining.Store(true)
}

func (s *Server) handleLive(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if s.draining.Load() || s.store.Ready(r.Context()) != nil {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-tar" {
		http.Error(w, "Content-Type must be application/x-tar", http.StatusUnsupportedMediaType)
		return
	}
	suppliedName := r.URL.Query().Get("name")
	name := names.Normalize(suppliedName)
	ctx := r.Context()
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("artifact.supplied_name", suppliedName),
		attribute.String("artifact.name", name),
	)
	attempt := store.PublicationAttempt{}
	if name != "" {
		attempt, err = s.store.BeginPublicationAttempt(ctx, name)
		if err != nil {
			http.Error(w, "publication failed", http.StatusInternalServerError)
			return
		}
		var finish, interrupt func()
		var active bool
		ctx, finish, interrupt, active = s.attempts.begin(ctx, name, attempt.Generation, requestAbort(w, r.Body))
		if !active {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.store.CompletePublicationAttempt(cleanupCtx, name, attempt.Token)
			cancel()
			http.Error(w, "Publication Attempt was superseded", http.StatusConflict)
			return
		}
		defer finish()

		monitorCtx, stopMonitoring := context.WithCancel(ctx)
		go s.monitorPublicationAttempt(monitorCtx, name, attempt.Token, interrupt)
		defer func() {
			stopMonitoring()
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.store.CompletePublicationAttempt(cleanupCtx, name, attempt.Token)
		}()
	}
	publication, err := s.store.Publish(ctx, name, attempt.Token, attempt.Generation, r.Body)
	if err != nil {
		if errors.Is(err, store.ErrPublicationSuperseded) || errors.Is(err, context.Canceled) {
			http.Error(w, "Publication Attempt was superseded", http.StatusConflict)
			return
		}
		http.Error(w, "publication failed", http.StatusBadRequest)
		return
	}
	span.SetAttributes(
		attribute.String("artifact.name", publication.Name),
		attribute.String("artifact.version.id", publication.CurrentVersionID),
		attribute.Int64("artifact.upload.encoded_bytes", publication.Stats.EncodedBytes),
		attribute.Int64("artifact.upload.extracted_bytes", publication.Stats.ExtractedBytes),
		attribute.Int64("artifact.upload.file_count", publication.Stats.FileCount),
	)
	writeJSON(w, http.StatusCreated, s.artifactResponse(publication.Artifact))
}

func requestAbort(w http.ResponseWriter, body io.Closer) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			if err := http.NewResponseController(w).SetReadDeadline(time.Now()); err != nil {
				go func() { _ = body.Close() }()
			}
		})
	}
}

func (s *Server) monitorPublicationAttempt(ctx context.Context, name, token string, interrupt func()) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current, err := s.store.PublicationAttemptCurrent(ctx, name, token)
			if err == nil && !current {
				interrupt()
				return
			}
		}
	}
}

func (s *Server) handleArtifactDirectory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	artifacts, err := s.store.ListArtifacts(r.Context())
	if err != nil {
		http.Error(w, "Artifact directory failed", http.StatusInternalServerError)
		return
	}
	response := make([]artifactResponse, 0, len(artifacts))
	for _, artifact := range artifacts {
		response = append(response, s.artifactResponse(artifact))
	}
	page, err := renderArtifactDirectory(response)
	if err != nil {
		http.Error(w, "Artifact directory failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
}

func (s *Server) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	artifacts, err := s.store.ListArtifacts(r.Context())
	if err != nil {
		http.Error(w, "Artifact listing failed", http.StatusInternalServerError)
		return
	}
	response := make([]artifactResponse, 0, len(artifacts))
	for _, artifact := range artifacts {
		response = append(response, s.artifactResponse(artifact))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleInspect(w http.ResponseWriter, r *http.Request) {
	artifact, err := s.store.Get(r.Context(), strings.ToLower(r.PathValue("name")))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, s.artifactResponse(artifact))
}

func (s *Server) handleDeleteArtifact(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(r.PathValue("name"))
	trace.SpanFromContext(r.Context()).SetAttributes(attribute.String("artifact.name", name))
	s.attempts.cancel(name)
	err := s.store.DeleteArtifact(r.Context(), name)
	if err != nil {
		if store.IsNotFound(err) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "Artifact deletion failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListVersions(w http.ResponseWriter, r *http.Request) {
	versions, err := s.store.ListVersions(r.Context(), strings.ToLower(r.PathValue("name")))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, versions)
}

func (s *Server) handleDeleteVersion(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(r.PathValue("name"))
	versionID := r.PathValue("versionID")
	trace.SpanFromContext(r.Context()).SetAttributes(
		attribute.String("artifact.name", name),
		attribute.String("artifact.version.id", versionID),
	)
	err := s.store.DeleteVersion(r.Context(), name, versionID)
	if err != nil {
		if store.IsNotFound(err) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "Version deletion failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/")
	name, relative, hasSlash := strings.Cut(trimmed, "/")
	if name == "" {
		http.NotFound(w, r)
		return
	}
	artifact, err := s.store.Get(r.Context(), strings.ToLower(name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	trace.SpanFromContext(r.Context()).SetAttributes(
		attribute.String("artifact.name", artifact.Name),
		attribute.String("artifact.version.id", artifact.CurrentVersionID),
	)
	w.Header().Set("Cache-Control", "no-store")
	if !hasSlash {
		http.Redirect(w, r, "/"+artifact.Name+"/", http.StatusPermanentRedirect)
		return
	}

	root := s.store.VersionPath(artifact.CurrentVersionID)
	if redirect, ok := canonicalRedirect(root, relative); ok {
		http.Redirect(w, r, "/"+artifact.Name+"/"+redirect, http.StatusPermanentRedirect)
		return
	}
	filePath, displayName, ok := resolveFile(root, relative)
	if !ok {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(filePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, displayName, info.ModTime(), file)
}

func canonicalRedirect(root, relative string) (string, bool) {
	if strings.HasSuffix(relative, ".html") {
		withoutExtension := strings.TrimSuffix(relative, ".html")
		if fileExists(root, relative) {
			if filepath.Base(relative) == "index.html" {
				parent := strings.TrimSuffix(strings.TrimSuffix(relative, "index.html"), "/")
				if parent == "" {
					return "", true
				}
				return parent + "/", true
			}
			return withoutExtension, true
		}
	}
	if filepath.Base(relative) == "index" {
		candidate := strings.TrimSuffix(relative, "index") + "index.html"
		if fileExists(root, candidate) {
			parent := strings.TrimSuffix(strings.TrimSuffix(relative, "index"), "/")
			if parent == "" {
				return "", true
			}
			return parent + "/", true
		}
	}
	return "", false
}

func resolveFile(root, relative string) (string, string, bool) {
	if relative == "" || strings.HasSuffix(relative, "/") {
		relative += "index.html"
	}
	if !safeRelativePath(relative) {
		return "", "", false
	}
	candidate := filepath.Join(root, filepath.FromSlash(relative))
	if fileExists(root, relative) {
		return candidate, filepath.Base(relative), true
	}
	if filepath.Ext(relative) == "" && fileExists(root, relative+".html") {
		return candidate + ".html", filepath.Base(relative) + ".html", true
	}
	return "", "", false
}

func fileExists(root, relative string) bool {
	if !safeRelativePath(relative) {
		return false
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative)))
	return err == nil && info.Mode().IsRegular()
}

func safeRelativePath(value string) bool {
	if value == "" || strings.Contains(value, "\\") {
		return false
	}
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	return cleaned == value && cleaned != "." && !strings.HasPrefix(cleaned, "../") && !filepath.IsAbs(value)
}

func (s *Server) artifactResponse(artifact store.Artifact) artifactResponse {
	return artifactResponse{
		Name:             artifact.Name,
		URL:              fmt.Sprintf("%s/%s/", s.publicURL, artifact.Name),
		CurrentVersionID: artifact.CurrentVersionID,
		PublishedAt:      artifact.PublishedAt,
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		return
	}
}
