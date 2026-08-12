# Published Artifact Service

The Published Artifact Service stores retained immutable Artifact Versions and serves the current Version of each Published Artifact as a static file tree.

## Server

Build and test the server from this directory:

```sh
go test ./...
go build ./cmd/artifact-server
```

The server uses these environment variables:

| Variable | Default |
| --- | --- |
| `ARTIFACT_LISTEN_ADDR` | `:8080` |
| `ARTIFACT_PUBLIC_BASE_URL` | Required |
| `ARTIFACT_DATABASE_PATH` | `/var/lib/artifact/database/artifacts.db` |
| `ARTIFACT_VERSIONS_PATH` | `/var/lib/artifact/versions` |
| `ARTIFACT_UPLOAD_TIMEOUT` | `1h` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `datadog-agent.datadog.svc.cluster.local:4317` |

The process applies checked-in SQLite migrations before it becomes ready. `/livez` reports process liveness. `/readyz` also checks the catalog and storage roots and becomes unavailable during graceful shutdown.

## HTTP API

Publication uses an `application/x-tar` request body:

```text
POST   /api/v1/artifacts?name=<optional>
GET    /api/v1/artifacts
GET    /api/v1/artifacts/{name}
DELETE /api/v1/artifacts/{name}
GET    /api/v1/artifacts/{name}/versions
DELETE /api/v1/artifacts/{name}/versions/{version-id}
```

Management responses use JSON. A publication and an Artifact inspection return:

```json
{
  "name": "example",
  "url": "https://artifacts.home.arpa/example/",
  "current_version_id": "019ff313-f945-7b22-a111-42b7672d94ea",
  "published_at": "2026-08-11T23:06:40Z"
}
```

The static tree is available below `/{name}/`. Artifact Name lookup is case-insensitive. Paths inside one Artifact Version are case-sensitive. Static responses use `Cache-Control: no-store`.

## CLI

Install or upgrade the `artifact` CLI independently of this repository on Linux or macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/eli0shin/artifacts/main/apps/published-artifact/install.sh | bash
```

The installer selects the current Linux or macOS x64 or arm64 release asset and installs it at `$HOME/.local/bin/artifact`. Add `$HOME/.local/bin` to `PATH` if the installer reports that it is missing.

Configure the service URL after installation:

```sh
artifact config set-url https://artifacts.example.com
```

The CLI reads `${XDG_CONFIG_HOME:-$HOME/.config}/artifact/config.json`. `ARTIFACT_CONFIG_PATH` selects a different file, and `ARTIFACT_SERVICE_URL` overrides the configured URL. There is no default URL.

```text
artifact config set-url <url>
artifact config get-url
artifact publish <path> [--name <name>]
artifact list
artifact inspect <name>
artifact delete <name>
artifact version list <name>
artifact version delete <name> <version-id>
artifact --version
```

`publish` prints the resulting URL. List and inspection commands use stable tab-separated output. Successful deletion commands print nothing.

The CLI has a separate semantic release stream with Git tags such as `artifact-v1.2.3` and four raw GitHub Release binaries for Linux and macOS on x64 and arm64.

## Generated catalog code

SQL queries live in `internal/store/queries.sql`. Regenerate `internal/store/catalogdb/` with:

```sh
go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0 generate
```
