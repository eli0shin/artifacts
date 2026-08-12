package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
}

type Artifact struct {
	Name             string `json:"name"`
	URL              string `json:"url"`
	CurrentVersionID string `json:"current_version_id"`
	PublishedAt      string `json:"published_at"`
}

type Version struct {
	ID          string `json:"id"`
	PublishedAt string `json:"published_at"`
	Current     bool   `json:"current"`
}

func New(baseURL string) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), httpClient: http.DefaultClient}
}

func (c *Client) ListArtifacts(ctx context.Context) ([]Artifact, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/artifacts", nil)
	if err != nil {
		return nil, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("list Artifacts: %w", err)
	}
	defer response.Body.Close()
	if err := responseError(response); err != nil {
		return nil, err
	}
	var artifacts []Artifact
	if err := json.NewDecoder(response.Body).Decode(&artifacts); err != nil {
		return nil, fmt.Errorf("decode Artifact list: %w", err)
	}
	return artifacts, nil
}

func (c *Client) Inspect(ctx context.Context, name string) (Artifact, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.artifactEndpoint(name), nil)
	if err != nil {
		return Artifact{}, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return Artifact{}, fmt.Errorf("inspect Artifact: %w", err)
	}
	defer response.Body.Close()
	if err := responseError(response); err != nil {
		return Artifact{}, err
	}
	var artifact Artifact
	if err := json.NewDecoder(response.Body).Decode(&artifact); err != nil {
		return Artifact{}, fmt.Errorf("decode Artifact inspection: %w", err)
	}
	return artifact, nil
}

func (c *Client) ListVersions(ctx context.Context, name string) ([]Version, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.artifactEndpoint(name)+"/versions", nil)
	if err != nil {
		return nil, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("list Versions: %w", err)
	}
	defer response.Body.Close()
	if err := responseError(response); err != nil {
		return nil, err
	}
	var versions []Version
	if err := json.NewDecoder(response.Body).Decode(&versions); err != nil {
		return nil, fmt.Errorf("decode Version list: %w", err)
	}
	return versions, nil
}

func (c *Client) DeleteVersion(ctx context.Context, name, versionID string) error {
	endpoint := c.artifactEndpoint(name) + "/versions/" + url.PathEscape(versionID)
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("delete Version: %w", err)
	}
	defer response.Body.Close()
	return responseError(response)
}

func (c *Client) DeleteArtifact(ctx context.Context, name string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.artifactEndpoint(name), nil)
	if err != nil {
		return err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("delete Artifact: %w", err)
	}
	defer response.Body.Close()
	return responseError(response)
}

func (c *Client) Publish(ctx context.Context, name string, body io.Reader) (Artifact, error) {
	endpoint := c.baseURL + "/api/v1/artifacts"
	if name != "" {
		endpoint += "?" + url.Values{"name": []string{name}}.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return Artifact{}, err
	}
	request.Header.Set("Content-Type", "application/x-tar")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return Artifact{}, fmt.Errorf("publish Artifact: %w", err)
	}
	defer response.Body.Close()
	if err := responseError(response); err != nil {
		return Artifact{}, err
	}
	var artifact Artifact
	if err := json.NewDecoder(response.Body).Decode(&artifact); err != nil {
		return Artifact{}, fmt.Errorf("decode publication response: %w", err)
	}
	return artifact, nil
}

func (c *Client) artifactEndpoint(name string) string {
	return c.baseURL + "/api/v1/artifacts/" + url.PathEscape(name)
}

func responseError(response *http.Response) error {
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(response.Body)
	message := strings.TrimSpace(string(body))
	if message == "" {
		return fmt.Errorf("request failed: %s", response.Status)
	}
	return fmt.Errorf("request failed: %s: %s", response.Status, message)
}
