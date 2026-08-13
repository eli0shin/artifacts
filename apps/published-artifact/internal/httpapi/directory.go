package httpapi

import (
	"bytes"
	"fmt"
	"html/template"
	"time"
)

type artifactDirectory struct {
	Artifacts []artifactDirectoryEntry
}

type artifactDirectoryEntry struct {
	Name             string
	URL              string
	PublishedAt      string
	PublishedAtLabel string
}

var artifactDirectoryTemplate = template.Must(template.New("artifact-directory").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Artifacts</title>
  <style>
    :root {
      color-scheme: light dark;
      font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      background: #f5f6f8;
      color: #1b1d22;
    }
    body {
      margin: 0;
    }
    main {
      width: min(52rem, calc(100% - 2rem));
      margin: 0 auto;
      padding: 4rem 0;
    }
    h1 {
      margin: 0 0 2rem;
      font-size: clamp(2rem, 7vw, 3.5rem);
      letter-spacing: -0.04em;
    }
    .artifact-list {
      margin: 0;
      padding: 0;
      overflow: hidden;
      list-style: none;
      border: 1px solid #d8dbe2;
      border-radius: 0.75rem;
      background: #fff;
    }
    .artifact-list li {
      display: grid;
      grid-template-columns: minmax(0, 1fr) auto;
      gap: 1rem;
      align-items: center;
      padding: 1rem 1.25rem;
    }
    .artifact-list li + li {
      border-top: 1px solid #e3e5ea;
    }
    .artifact-list a {
      width: fit-content;
      max-width: 100%;
      overflow-wrap: anywhere;
      color: #1457d9;
      font-size: 1.05rem;
      font-weight: 650;
      text-decoration-thickness: 0.08em;
      text-underline-offset: 0.18em;
    }
    .artifact-list a:hover {
      text-decoration-thickness: 0.14em;
    }
    .artifact-list a:focus-visible {
      border-radius: 0.15rem;
      outline: 0.2rem solid #f4b942;
      outline-offset: 0.2rem;
    }
    time,
    .empty-state {
      color: #626773;
    }
    time {
      font-size: 0.9rem;
      white-space: nowrap;
    }
    .empty-state {
      margin: 0;
      padding: 2rem;
      border: 1px dashed #c8ccd5;
      border-radius: 0.75rem;
      text-align: center;
    }
    @media (max-width: 36rem) {
      main {
        padding: 2.5rem 0;
      }
      .artifact-list li {
        grid-template-columns: 1fr;
        gap: 0.35rem;
      }
    }
    @media (prefers-color-scheme: dark) {
      :root {
        background: #15171b;
        color: #f0f1f3;
      }
      .artifact-list {
        border-color: #3c4049;
        background: #202329;
      }
      .artifact-list li + li {
        border-color: #353942;
      }
      .artifact-list a {
        color: #8bb5ff;
      }
      time,
      .empty-state {
        color: #b4b8c2;
      }
      .empty-state {
        border-color: #4a4f59;
      }
    }
  </style>
</head>
<body>
  <main>
    <h1>Artifacts</h1>
    {{if .Artifacts}}
    <ul class="artifact-list" aria-label="Published artifacts">
      {{range .Artifacts}}<li>
        <a href="{{.URL}}" target="_blank" rel="noopener">{{.Name}}</a>
        <time datetime="{{.PublishedAt}}">{{.PublishedAtLabel}}</time>
      </li>{{end}}
    </ul>
    {{else}}
    <p class="empty-state">No artifacts have been published yet.</p>
    {{end}}
  </main>
</body>
</html>
`))

func renderArtifactDirectory(artifacts []artifactResponse) ([]byte, error) {
	entries := make([]artifactDirectoryEntry, 0, len(artifacts))
	for _, artifact := range artifacts {
		publishedAt, err := time.Parse(time.RFC3339Nano, artifact.PublishedAt)
		if err != nil {
			return nil, fmt.Errorf("parse Artifact publication time: %w", err)
		}
		entries = append(entries, artifactDirectoryEntry{
			Name:             artifact.Name,
			URL:              artifact.URL,
			PublishedAt:      artifact.PublishedAt,
			PublishedAtLabel: publishedAt.UTC().Format("2 Jan 2006, 15:04 UTC"),
		})
	}

	var page bytes.Buffer
	if err := artifactDirectoryTemplate.Execute(&page, artifactDirectory{Artifacts: entries}); err != nil {
		return nil, fmt.Errorf("render Artifact Directory: %w", err)
	}
	return page.Bytes(), nil
}
