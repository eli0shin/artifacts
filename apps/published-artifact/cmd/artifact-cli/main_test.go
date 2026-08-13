package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/eli0shin/artifacts/apps/published-artifact/internal/httpapi"
	"github.com/eli0shin/artifacts/apps/published-artifact/internal/store"
)

var artifactBinary string

func TestMain(m *testing.M) {
	artifactBinary = os.Getenv("ARTIFACT_CLI_BINARY")
	if artifactBinary == "" {
		temporaryDirectory, err := os.MkdirTemp("", "artifact-cli-test-")
		if err != nil {
			panic(err)
		}
		artifactBinary = filepath.Join(temporaryDirectory, "artifact")
		command := exec.Command("go", "build", "-o", artifactBinary, ".")
		command.Stdout = os.Stderr
		command.Stderr = os.Stderr
		if err := command.Run(); err != nil {
			os.Exit(1)
		}
		status := m.Run()
		_ = os.RemoveAll(temporaryDirectory)
		os.Exit(status)
	}
	os.Exit(m.Run())
}

func TestConfigSetAndGetURL(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "nested", "config.json")
	t.Setenv("ARTIFACT_CONFIG_PATH", configPath)
	t.Setenv("ARTIFACT_SERVICE_URL", "")

	var stdout strings.Builder
	if err := execute(t.Context(), []string{"config", "set-url", "not-validated"}, &stdout); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "" {
		t.Fatalf("set-url output = %q", stdout.String())
	}
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "{\"service_url\":\"not-validated\"}\n" {
		t.Fatalf("config contents = %q", contents)
	}

	if err := execute(t.Context(), []string{"config", "get-url"}, &stdout); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "not-validated\n" {
		t.Fatalf("get-url output = %q", stdout.String())
	}
}

func TestEnvironmentServiceURLOverridesConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("ARTIFACT_CONFIG_PATH", configPath)
	t.Setenv("ARTIFACT_SERVICE_URL", "from-environment")
	if err := os.WriteFile(configPath, []byte(`{"service_url":"from-file"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	if err := execute(t.Context(), []string{"config", "get-url"}, &stdout); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "from-environment\n" {
		t.Fatalf("get-url output = %q", stdout.String())
	}
}

func TestNetworkCommandRequiresServiceURL(t *testing.T) {
	t.Setenv("ARTIFACT_CONFIG_PATH", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("ARTIFACT_SERVICE_URL", "")
	if err := execute(t.Context(), []string{"list"}, io.Discard); err == nil {
		t.Fatal("list succeeded without a service URL")
	}
}

func TestInvalidCommandDoesNotRequireConfiguration(t *testing.T) {
	t.Setenv("ARTIFACT_CONFIG_PATH", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("ARTIFACT_SERVICE_URL", "")
	if err := execute(t.Context(), []string{"unknown"}, io.Discard); err == nil || err.Error() != `unknown command "unknown" for "artifact"` {
		t.Fatalf("unknown command error = %v", err)
	}
	if err := execute(t.Context(), []string{"list", "extra"}, io.Discard); err == nil || err.Error() != `unknown command "extra" for "artifact list"` {
		t.Fatalf("invalid list error = %v", err)
	}
}

func TestPublishPrintsResultingURLAndServesSourceTree(t *testing.T) {
	serviceURL := startArtifactService(t)
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("published\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, status := runArtifact(t, serviceURL, "publish", source, "--name", "My Report")
	if stdout != serviceURL+"/my-report/\n" || stderr != "" || status != 0 {
		t.Fatalf("artifact publish result = stdout %q, stderr %q, status %d", stdout, stderr, status)
	}

	response, err := http.Get(serviceURL + "/my-report/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || string(body) != "published\n" {
		t.Fatalf("published source = status %d, body %q", response.StatusCode, body)
	}
}

func TestPublishSingleFileUsesGeneratedNameAndRetainsBasename(t *testing.T) {
	serviceURL := startArtifactService(t)
	source := filepath.Join(t.TempDir(), "Report File.txt")
	if err := os.WriteFile(source, []byte("single file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, status := runArtifact(t, serviceURL, "publish", source)
	publicationPattern := regexp.MustCompile(`^` + regexp.QuoteMeta(serviceURL) + `/([a-z0-9]+(?:-[a-z0-9]+)+)/\n$`)
	matches := publicationPattern.FindStringSubmatch(stdout)
	if matches == nil || stderr != "" || status != 0 {
		t.Fatalf("generated publication = stdout %q, stderr %q, status %d", stdout, stderr, status)
	}

	response, err := http.Get(serviceURL + "/" + matches[1] + "/Report%20File.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || string(body) != "single file\n" {
		t.Fatalf("published file = status %d, body %q", response.StatusCode, body)
	}
}

func TestListAndInspectPrintStableTabSeparatedOutput(t *testing.T) {
	serviceURL := startArtifactService(t)
	source := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(source, []byte("report\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, status := runArtifact(t, serviceURL, "publish", source, "--name", "Zulu")
	if stdout != serviceURL+"/zulu/\n" || stderr != "" || status != 0 {
		t.Fatalf("artifact publish result = stdout %q, stderr %q, status %d", stdout, stderr, status)
	}
	stdout, stderr, status = runArtifact(t, serviceURL, "publish", source, "--name", "Alpha")
	if stdout != serviceURL+"/alpha/\n" || stderr != "" || status != 0 {
		t.Fatalf("artifact publish result = stdout %q, stderr %q, status %d", stdout, stderr, status)
	}

	stdout, stderr, status = runArtifact(t, serviceURL, "list")
	expectedList := "alpha\t" + serviceURL + "/alpha/\n" + "zulu\t" + serviceURL + "/zulu/\n"
	if stdout != expectedList || stderr != "" || status != 0 {
		t.Fatalf("artifact list result = stdout %q, stderr %q, status %d", stdout, stderr, status)
	}

	stdout, stderr, status = runArtifact(t, serviceURL, "inspect", "ALPHA")
	inspectPattern := regexp.MustCompile(`^alpha\t` + regexp.QuoteMeta(serviceURL) + `/alpha/\t\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?Z\n$`)
	if !inspectPattern.MatchString(stdout) || stderr != "" || status != 0 {
		t.Fatalf("artifact inspect result = stdout %q, stderr %q, status %d", stdout, stderr, status)
	}
}

func TestDeleteRemovesArtifactWithoutSuccessOutput(t *testing.T) {
	serviceURL := startArtifactService(t)
	source := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(source, []byte("report\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, status := runArtifact(t, serviceURL, "publish", source, "--name", "Report")
	if stdout != serviceURL+"/report/\n" || stderr != "" || status != 0 {
		t.Fatalf("artifact publish result = stdout %q, stderr %q, status %d", stdout, stderr, status)
	}

	stdout, stderr, status = runArtifact(t, serviceURL, "delete", "REPORT")
	if stdout != "" || stderr != "" || status != 0 {
		t.Fatalf("artifact delete result = stdout %q, stderr %q, status %d", stdout, stderr, status)
	}

	stdout, stderr, status = runArtifact(t, serviceURL, "inspect", "report")
	expectedError := "artifact: request failed: 404 Not Found: 404 page not found\n"
	if stdout != "" || stderr != expectedError || status == 0 {
		t.Fatalf("missing Artifact inspection = stdout %q, stderr %q, status %d", stdout, stderr, status)
	}
}

func TestVersionListAndDeleteManageRetainedVersions(t *testing.T) {
	serviceURL := startArtifactService(t)
	source := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(source, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"one\n", "two\n"} {
		if err := os.WriteFile(source, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		stdout, stderr, status := runArtifact(t, serviceURL, "publish", source, "--name", "Report")
		if stdout != serviceURL+"/report/\n" || stderr != "" || status != 0 {
			t.Fatalf("artifact publish result = stdout %q, stderr %q, status %d", stdout, stderr, status)
		}
	}

	stdout, stderr, status := runArtifact(t, serviceURL, "version", "list", "REPORT")
	versionID := `([0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12})`
	timestamp := `(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?Z)`
	listPattern := regexp.MustCompile(`^` + versionID + `\t` + timestamp + `\tcurrent\n` + versionID + `\t` + timestamp + `\n$`)
	matches := listPattern.FindStringSubmatch(stdout)
	if matches == nil || stderr != "" || status != 0 {
		t.Fatalf("artifact version list result = stdout %q, stderr %q, status %d", stdout, stderr, status)
	}
	currentID := matches[1]
	olderID := matches[3]
	olderTimestamp := matches[4]

	stdout, stderr, status = runArtifact(t, serviceURL, "version", "delete", "report", currentID)
	if stdout != "" || stderr != "" || status != 0 {
		t.Fatalf("artifact version delete result = stdout %q, stderr %q, status %d", stdout, stderr, status)
	}
	stdout, stderr, status = runArtifact(t, serviceURL, "version", "list", "report")
	expectedList := olderID + "\t" + olderTimestamp + "\tcurrent\n"
	if stdout != expectedList || stderr != "" || status != 0 {
		t.Fatalf("artifact version list after deletion = stdout %q, stderr %q, status %d", stdout, stderr, status)
	}

	stdout, stderr, status = runArtifact(t, serviceURL, "version", "delete", "report", olderID)
	if stdout != "" || stderr != "" || status != 0 {
		t.Fatalf("last Version deletion = stdout %q, stderr %q, status %d", stdout, stderr, status)
	}
	stdout, stderr, status = runArtifact(t, serviceURL, "inspect", "report")
	if stdout != "" || stderr != "artifact: request failed: 404 Not Found: 404 page not found\n" || status == 0 {
		t.Fatalf("inspection after last Version deletion = stdout %q, stderr %q, status %d", stdout, stderr, status)
	}
}

func TestVersionFlagsAndCommandErrorsHaveCompleteOutput(t *testing.T) {
	for _, flag := range []string{"--version", "-v"} {
		stdout, stderr, status := runArtifact(t, "http://unused.invalid", flag)
		if !regexp.MustCompile(`^artifact (?:dev|\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?)\n$`).MatchString(stdout) || stderr != "" || status != 0 {
			t.Fatalf("artifact %s result = stdout %q, stderr %q, status %d", flag, stdout, stderr, status)
		}
	}

	stdout, stderr, status := runArtifact(t, "http://unused.invalid")
	if !strings.Contains(stdout, "Usage:\n  artifact [command]") || stderr != "" || status != 0 {
		t.Fatalf("artifact without command = stdout %q, stderr %q, status %d", stdout, stderr, status)
	}

	stdout, stderr, status = runArtifact(t, "http://unused.invalid", "publish", "source", "--unknown")
	if stdout != "" || stderr != "artifact: unknown flag: --unknown\n" || status == 0 {
		t.Fatalf("artifact with unknown option = stdout %q, stderr %q, status %d", stdout, stderr, status)
	}
}

func TestHelpFlagsPrintCommandHelpWithoutConfiguration(t *testing.T) {
	t.Setenv("ARTIFACT_CONFIG_PATH", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("ARTIFACT_SERVICE_URL", "")

	checks := []struct {
		arguments []string
		contains  []string
	}{
		{[]string{"--help"}, []string{"Usage:\n  artifact [command]", "-h, --help"}},
		{[]string{"publish", "--help"}, []string{"artifact publish <path>", "--name string"}},
		{[]string{"version", "delete", "--help"}, []string{"artifact version delete <name> <version-id>"}},
	}
	for _, check := range checks {
		var stdout strings.Builder
		if err := execute(t.Context(), check.arguments, &stdout); err != nil {
			t.Fatalf("artifact %v: %v", check.arguments, err)
		}
		for _, expected := range check.contains {
			if !strings.Contains(stdout.String(), expected) {
				t.Errorf("artifact %v output %q does not contain %q", check.arguments, stdout.String(), expected)
			}
		}
	}
}

func TestMissingPublicationSourceFailsWithoutSuccessOutput(t *testing.T) {
	serviceURL := startArtifactService(t)
	missing := filepath.Join(t.TempDir(), "missing")
	stdout, stderr, status := runArtifact(t, serviceURL, "publish", missing)
	expectedError := "artifact: open source: lstat " + missing + ": no such file or directory\n"
	if stdout != "" || stderr != expectedError || status == 0 {
		t.Fatalf("missing publication source = stdout %q, stderr %q, status %d", stdout, stderr, status)
	}
}

func TestRejectedPublicationReportsServiceError(t *testing.T) {
	serviceURL := startArtifactService(t)
	source := t.TempDir()
	if err := os.Symlink("target", filepath.Join(source, "00-unsupported-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "large.bin"), make([]byte, 4<<20), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, status := runArtifact(t, serviceURL, "publish", source)
	expectedError := "artifact: request failed: 400 Bad Request: publication failed\n"
	if stdout != "" || stderr != expectedError || status == 0 {
		t.Fatalf("rejected publication = stdout %q, stderr %q, status %d", stdout, stderr, status)
	}
}

func TestRequestFailurePrintsOnlyError(t *testing.T) {
	serviceURL := startArtifactServiceWith(t, func(server *httpapi.Server) { server.Drain() })

	stdout, stderr, status := runArtifact(t, serviceURL, "list")
	expectedError := "artifact: request failed: 503 Service Unavailable: server is draining\n"
	if stdout != "" || stderr != expectedError || status == 0 {
		t.Fatalf("failed request = stdout %q, stderr %q, status %d", stdout, stderr, status)
	}
}

func startArtifactService(t *testing.T) string {
	t.Helper()
	return startArtifactServiceWith(t, nil)
}

func startArtifactServiceWith(t *testing.T, configure func(*httpapi.Server)) string {
	t.Helper()
	root := t.TempDir()
	catalog, err := store.Open(t.Context(), filepath.Join(root, "database", "artifacts.db"), filepath.Join(root, "versions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close() })

	httpServer := httptest.NewUnstartedServer(nil)
	serviceURL := "http://" + httpServer.Listener.Addr().String()
	artifactServer := httpapi.New(catalog, serviceURL)
	if configure != nil {
		configure(artifactServer)
	}
	httpServer.Config.Handler = artifactServer
	httpServer.Start()
	t.Cleanup(httpServer.Close)
	return httpServer.URL
}

func runArtifact(t *testing.T, serviceURL string, arguments ...string) (string, string, int) {
	t.Helper()
	command := exec.CommandContext(context.Background(), artifactBinary, arguments...)
	command.Env = append(os.Environ(), "ARTIFACT_SERVICE_URL="+serviceURL)
	var stdout strings.Builder
	var stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	if exitError, ok := err.(*exec.ExitError); ok {
		return stdout.String(), stderr.String(), exitError.ExitCode()
	}
	t.Fatal(err)
	return "", "", -1
}
