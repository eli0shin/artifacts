package httpapi_test

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eli0shin/artifacts/apps/published-artifact/internal/httpapi"
	"github.com/eli0shin/artifacts/apps/published-artifact/internal/store"
)

func TestArtifactDirectoryShowsEmptyState(t *testing.T) {
	handler := newTestHandler(t)

	response := request(t, handler, http.MethodGet, "/", nil)

	if response.Code != http.StatusOK {
		t.Fatalf("directory status = %d, want %d; body: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8", response.Header().Get("Content-Type"))
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
	}
	body := response.Body.String()
	for _, expected := range []string{"<h1>Artifacts</h1>", "No artifacts have been published yet."} {
		if !strings.Contains(body, expected) {
			t.Errorf("directory body does not contain %q", expected)
		}
	}
	if strings.Contains(body, "0 published artifacts") {
		t.Error("directory shows an Artifact count")
	}
}

func TestArtifactDirectoryListsArtifactsAlphabeticallyWithPublicationTimes(t *testing.T) {
	handler := newTestHandler(t)
	publish(t, handler, "Zulu", map[string]string{"index.html": "last"})
	publish(t, handler, "Alpha", map[string]string{"index.html": "first"})

	response := request(t, handler, http.MethodGet, "/", nil)

	if response.Code != http.StatusOK {
		t.Fatalf("directory status = %d, want %d; body: %s", response.Code, http.StatusOK, response.Body.String())
	}
	body := response.Body.String()
	alpha := strings.Index(body, `href="https://artifacts.home.arpa/alpha/"`)
	zulu := strings.Index(body, `href="https://artifacts.home.arpa/zulu/"`)
	if alpha < 0 || zulu < 0 || alpha >= zulu {
		t.Fatalf("directory entries are missing or not alphabetical: alpha=%d zulu=%d", alpha, zulu)
	}
	for _, expected := range []string{
		`target="_blank"`,
		`rel="noopener"`,
		`<time datetime="`,
		` UTC</time>`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("directory body does not contain %q", expected)
		}
	}
	if strings.Contains(body, "current_version_id") {
		t.Error("directory exposes a Version ID")
	}
}

func TestGracefulDrainBecomesUnreadyAndRejectsNewPublications(t *testing.T) {
	server := newTestHandler(t)
	server.Drain()

	ready := request(t, server, http.MethodGet, "/readyz", nil)
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status = %d, want %d", ready.Code, http.StatusServiceUnavailable)
	}
	published := tarRequest(t, server, "/api/v1/artifacts?name=late", tarBody(t, map[string]string{"index.html": "late"}))
	if published.Code != http.StatusServiceUnavailable {
		t.Fatalf("publish while draining status = %d, want %d", published.Code, http.StatusServiceUnavailable)
	}
}

func TestHealthEndpointsReportReadyService(t *testing.T) {
	handler := newTestHandler(t)

	for _, path := range []string{"/livez", "/readyz"} {
		response := request(t, handler, http.MethodGet, path, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want %d; body: %s", path, response.Code, http.StatusOK, response.Body.String())
		}
		if response.Body.String() != "ok\n" {
			t.Errorf("GET %s body = %q, want %q", path, response.Body.String(), "ok\n")
		}
	}
}

func TestPublishMakesNormalizedArtifactAvailableAtCanonicalRoutes(t *testing.T) {
	handler := newTestHandler(t)
	archive := tarBody(t, map[string]string{
		"index.html":      "<h1>Home</h1>",
		"docs/index.html": "<h1>Docs</h1>",
		"docs/about.html": "<h1>About</h1>",
		"Case.TXT":        "case-sensitive",
	})

	published := tarRequest(t, handler, "/api/v1/artifacts?name=My%20Demo!!!", archive)
	if published.Code != http.StatusCreated {
		t.Fatalf("publish status = %d, want %d; body: %s", published.Code, http.StatusCreated, published.Body.String())
	}
	var artifact struct {
		Name        string `json:"name"`
		URL         string `json:"url"`
		PublishedAt string `json:"published_at"`
	}
	if err := json.Unmarshal(published.Body.Bytes(), &artifact); err != nil {
		t.Fatal(err)
	}
	if artifact.Name != "my-demo" || artifact.URL != "https://artifacts.home.arpa/my-demo/" || artifact.PublishedAt == "" {
		t.Fatalf("published artifact = %#v", artifact)
	}

	rootRedirect := request(t, handler, http.MethodGet, "/my-demo", nil)
	if rootRedirect.Code != http.StatusPermanentRedirect || rootRedirect.Header().Get("Location") != "/my-demo/" {
		t.Fatalf("root redirect = %d %q", rootRedirect.Code, rootRedirect.Header().Get("Location"))
	}

	root := request(t, handler, http.MethodGet, "/MY-DEMO/", nil)
	if root.Code != http.StatusOK || root.Body.String() != "<h1>Home</h1>" {
		t.Fatalf("root response = %d %q", root.Code, root.Body.String())
	}
	if root.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", root.Header().Get("Cache-Control"))
	}

	cleanHTML := request(t, handler, http.MethodGet, "/my-demo/docs/about", nil)
	if cleanHTML.Code != http.StatusOK || cleanHTML.Body.String() != "<h1>About</h1>" {
		t.Fatalf("clean HTML response = %d %q", cleanHTML.Code, cleanHTML.Body.String())
	}

	redirect := request(t, handler, http.MethodGet, "/my-demo/docs/about.html", nil)
	if redirect.Code != http.StatusPermanentRedirect || redirect.Header().Get("Location") != "/my-demo/docs/about" {
		t.Fatalf("HTML redirect = %d %q", redirect.Code, redirect.Header().Get("Location"))
	}
	for _, path := range []string{"/my-demo/docs/index", "/my-demo/docs/index.html"} {
		redirect = request(t, handler, http.MethodGet, path, nil)
		if redirect.Code != http.StatusPermanentRedirect || redirect.Header().Get("Location") != "/my-demo/docs/" {
			t.Fatalf("index redirect for %s = %d %q", path, redirect.Code, redirect.Header().Get("Location"))
		}
	}
	docs := request(t, handler, http.MethodGet, "/my-demo/docs/", nil)
	if docs.Code != http.StatusOK || docs.Body.String() != "<h1>Docs</h1>" {
		t.Fatalf("directory index response = %d %q", docs.Code, docs.Body.String())
	}
	wrongCase := request(t, handler, http.MethodGet, "/my-demo/case.TXT", nil)
	if wrongCase.Code != http.StatusNotFound {
		t.Fatalf("wrong-case path status = %d, want %d", wrongCase.Code, http.StatusNotFound)
	}
}

func TestPublishRejectsInvalidArtifactPaths(t *testing.T) {
	handler := newTestHandler(t)
	archive := tarBody(t, map[string]string{"../outside.txt": "not allowed"})
	response := tarRequest(t, handler, "/api/v1/artifacts?name=unsafe", archive)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("publish status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	missing := request(t, handler, http.MethodGet, "/api/v1/artifacts/unsafe", nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("invalid archive created an Artifact: status = %d", missing.Code)
	}
}

func TestNewPublicationOnOverlappingServerCancelsOlderNetworkUpload(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "database", "artifacts.db")
	versionsPath := filepath.Join(root, "versions")
	firstStore, err := store.Open(t.Context(), databasePath, versionsPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = firstStore.Close() })
	secondStore, err := store.Open(t.Context(), databasePath, versionsPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondStore.Close() })
	firstServer := httptest.NewServer(httpapi.New(firstStore, "https://artifacts.home.arpa"))
	t.Cleanup(firstServer.Close)
	secondServer := httptest.NewServer(httpapi.New(secondStore, "https://artifacts.home.arpa"))
	t.Cleanup(secondServer.Close)

	if status := networkPublish(t, firstServer, "shared", tarBody(t, map[string]string{"index.html": "original"})); status != http.StatusCreated {
		t.Fatalf("initial publication status = %d", status)
	}

	reader, writer := io.Pipe()
	defer writer.Close()
	olderDone := make(chan int, 1)
	go func() {
		request, requestErr := http.NewRequest(http.MethodPost, firstServer.URL+"/api/v1/artifacts?name=shared", reader)
		if requestErr != nil {
			olderDone <- 0
			return
		}
		request.Header.Set("Content-Type", "application/x-tar")
		response, requestErr := firstServer.Client().Do(request)
		if requestErr != nil {
			olderDone <- 0
			return
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, response.Body)
		olderDone <- response.StatusCode
	}()
	tarWriter := tar.NewWriter(writer)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "index.html", Mode: 0o644, Size: 1 << 30, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write([]byte("incomplete")); err != nil {
		t.Fatal(err)
	}
	registrationDeadline := time.Now().Add(2 * time.Second)
	for {
		registered, err := firstStore.PublicationAttemptExists(t.Context(), "shared")
		if err != nil {
			t.Fatal(err)
		}
		if registered {
			break
		}
		if time.Now().After(registrationDeadline) {
			t.Fatal("older Publication Attempt did not register")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if status := networkPublish(t, secondServer, "shared", tarBody(t, map[string]string{"index.html": "newer"})); status != http.StatusCreated {
		t.Fatalf("newer publication status = %d", status)
	}
	select {
	case status := <-olderDone:
		if status != http.StatusConflict {
			t.Fatalf("older network publication status = %d, want %d", status, http.StatusConflict)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("older network publication was not canceled")
	}

	response, err := secondServer.Client().Get(secondServer.URL + "/shared/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || string(contents) != "newer" {
		t.Fatalf("current response = %d %q", response.StatusCode, contents)
	}
}

func TestArtifactNameStartingWithAPIServesItsStaticTree(t *testing.T) {
	handler := newTestHandler(t)
	publish(t, handler, "api-guide", map[string]string{"index.html": "API guide"})
	response := request(t, handler, http.MethodGet, "/api-guide/", nil)
	if response.Code != http.StatusOK || response.Body.String() != "API guide" {
		t.Fatalf("static response = %d %q", response.Code, response.Body.String())
	}
}

func TestNewPublicationCancelsOlderAttemptWithoutDisplacingCurrentVersion(t *testing.T) {
	handler := newTestHandler(t)
	publish(t, handler, "demo", map[string]string{"index.html": "original"})

	reader, writer := io.Pipe()
	defer writer.Close()
	olderDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		olderDone <- tarRequest(t, handler, "/api/v1/artifacts?name=demo", reader)
	}()
	tarWriter := tar.NewWriter(writer)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "index.html", Mode: 0o644, Size: 1 << 30, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write([]byte("incomplete")); err != nil {
		t.Fatal(err)
	}

	publish(t, handler, "demo", map[string]string{"index.html": "newer"})
	select {
	case older := <-olderDone:
		if older.Code != http.StatusConflict {
			t.Fatalf("older Publication Attempt status = %d, want %d", older.Code, http.StatusConflict)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("older Publication Attempt was not canceled")
	}
	_ = tarWriter.Close()
	_ = writer.Close()

	current := request(t, handler, http.MethodGet, "/demo/", nil)
	if current.Code != http.StatusOK || current.Body.String() != "newer" {
		t.Fatalf("current response = %d %q", current.Code, current.Body.String())
	}

	failed := tarRequest(t, handler, "/api/v1/artifacts?name=demo", strings.NewReader("not a tar archive"))
	if failed.Code == http.StatusCreated {
		t.Fatal("invalid Publication Attempt succeeded")
	}
	current = request(t, handler, http.MethodGet, "/demo/", nil)
	if current.Code != http.StatusOK || current.Body.String() != "newer" {
		t.Fatalf("failed attempt displaced current Version: %d %q", current.Code, current.Body.String())
	}
}

func TestPublishRejectsNonTarMediaType(t *testing.T) {
	handler := newTestHandler(t)
	response := request(t, handler, http.MethodPost, "/api/v1/artifacts?name=demo", tarBody(t, map[string]string{"index.html": "content"}))
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("publish status = %d, want %d; body: %s", response.Code, http.StatusUnsupportedMediaType, response.Body.String())
	}
}

func TestGeneratedPublicationListInspectAndArtifactDeletion(t *testing.T) {
	handler := newTestHandler(t)
	first := publish(t, handler, "", map[string]string{"index.html": "first"})
	second := publish(t, handler, "", map[string]string{"index.html": "second"})
	if first.Name == second.Name || first.Name == "" || second.Name == "" {
		t.Fatalf("generated names = %q and %q", first.Name, second.Name)
	}

	listed := request(t, handler, http.MethodGet, "/api/v1/artifacts", nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("Artifact list status = %d; body: %s", listed.Code, listed.Body.String())
	}
	var artifacts []publishedArtifact
	if err := json.Unmarshal(listed.Body.Bytes(), &artifacts); err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 2 || artifacts[0].Name >= artifacts[1].Name {
		t.Fatalf("Artifacts are not alphabetical: %#v", artifacts)
	}

	inspected := request(t, handler, http.MethodGet, "/api/v1/artifacts/"+strings.ToUpper(first.Name), nil)
	if inspected.Code != http.StatusOK {
		t.Fatalf("inspect status = %d; body: %s", inspected.Code, inspected.Body.String())
	}

	deleted := request(t, handler, http.MethodDelete, "/api/v1/artifacts/"+first.Name, nil)
	if deleted.Code != http.StatusNoContent || deleted.Body.Len() != 0 {
		t.Fatalf("delete Artifact = %d %q", deleted.Code, deleted.Body.String())
	}
	missing := request(t, handler, http.MethodGet, "/"+first.Name+"/", nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("deleted Artifact static status = %d, want %d", missing.Code, http.StatusNotFound)
	}
	deletedAgain := request(t, handler, http.MethodDelete, "/api/v1/artifacts/"+first.Name, nil)
	if deletedAgain.Code != http.StatusNotFound {
		t.Fatalf("delete missing Artifact status = %d, want %d", deletedAgain.Code, http.StatusNotFound)
	}
}

func TestVersionLifecycleRetainsReplacesAndSelectsEarlierContent(t *testing.T) {
	handler := newTestHandler(t)
	first := publish(t, handler, "Release Notes", map[string]string{"index.html": "version one"})
	second := publish(t, handler, "Release Notes", map[string]string{"index.html": "version two"})
	if first.CurrentVersionID == second.CurrentVersionID {
		t.Fatal("replacement reused the Version ID")
	}

	current := request(t, handler, http.MethodGet, "/release-notes/", nil)
	if current.Code != http.StatusOK || current.Body.String() != "version two" {
		t.Fatalf("current response = %d %q", current.Code, current.Body.String())
	}

	listed := request(t, handler, http.MethodGet, "/api/v1/artifacts/release-notes/versions", nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("version list status = %d; body: %s", listed.Code, listed.Body.String())
	}
	var versions []struct {
		ID      string `json:"id"`
		Current bool   `json:"current"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &versions); err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].ID != second.CurrentVersionID || !versions[0].Current || versions[1].ID != first.CurrentVersionID || versions[1].Current {
		t.Fatalf("versions = %#v", versions)
	}

	deleted := request(t, handler, http.MethodDelete, "/api/v1/artifacts/release-notes/versions/"+second.CurrentVersionID, nil)
	if deleted.Code != http.StatusNoContent || deleted.Body.Len() != 0 {
		t.Fatalf("delete current Version = %d %q", deleted.Code, deleted.Body.String())
	}
	fallback := request(t, handler, http.MethodGet, "/release-notes/", nil)
	if fallback.Code != http.StatusOK || fallback.Body.String() != "version one" {
		t.Fatalf("fallback response = %d %q", fallback.Code, fallback.Body.String())
	}

	deleted = request(t, handler, http.MethodDelete, "/api/v1/artifacts/release-notes/versions/"+first.CurrentVersionID, nil)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete only Version status = %d; body: %s", deleted.Code, deleted.Body.String())
	}
	missing := request(t, handler, http.MethodGet, "/api/v1/artifacts/release-notes", nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("inspect deleted Artifact status = %d, want %d", missing.Code, http.StatusNotFound)
	}
}

type publishedArtifact struct {
	Name             string `json:"name"`
	URL              string `json:"url"`
	CurrentVersionID string `json:"current_version_id"`
	PublishedAt      string `json:"published_at"`
}

func publish(t *testing.T, handler http.Handler, name string, files map[string]string) publishedArtifact {
	t.Helper()
	target := "/api/v1/artifacts"
	if name != "" {
		target += "?name=" + url.QueryEscape(name)
	}
	response := tarRequest(t, handler, target, tarBody(t, files))
	if response.Code != http.StatusCreated {
		t.Fatalf("publish %q status = %d; body: %s", name, response.Code, response.Body.String())
	}
	var artifact publishedArtifact
	if err := json.Unmarshal(response.Body.Bytes(), &artifact); err != nil {
		t.Fatal(err)
	}
	return artifact
}

func networkPublish(t *testing.T, server *httptest.Server, name string, body io.Reader) int {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/artifacts?name="+url.QueryEscape(name), body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-tar")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode
}

func newTestHandler(t *testing.T) *httpapi.Server {
	t.Helper()
	root := t.TempDir()
	catalog, err := store.Open(t.Context(), filepath.Join(root, "database", "artifacts.db"), filepath.Join(root, "versions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close() })
	return httpapi.New(catalog, "https://artifacts.home.arpa")
}

func request(t *testing.T, handler http.Handler, method, target string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	return requestWithHeaders(t, handler, method, target, body, nil)
}

func tarRequest(t *testing.T, handler http.Handler, target string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	return requestWithHeaders(t, handler, http.MethodPost, target, body, map[string]string{"Content-Type": "application/x-tar"})
}

func requestWithHeaders(t *testing.T, handler http.Handler, method, target string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func tarBody(t *testing.T, files map[string]string) *bytes.Reader {
	t.Helper()
	var body bytes.Buffer
	writer := tar.NewWriter(&body)
	for name, contents := range files {
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(contents)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(body.Bytes())
}
