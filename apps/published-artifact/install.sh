#!/usr/bin/env bash
set -euo pipefail

repository="eli0shin/artifacts"
install_directory="${HOME}/.local/bin"
binary_name="artifact"

operating_system=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$operating_system" in
  darwin | linux) ;;
  *)
    echo "Unsupported OS: $operating_system"
    exit 1
    ;;
esac

architecture=$(uname -m)
case "$architecture" in
  x86_64) architecture="x64" ;;
  aarch64 | arm64) architecture="arm64" ;;
  *)
    echo "Unsupported architecture: $architecture"
    exit 1
    ;;
esac

asset="artifact-${operating_system}-${architecture}"
destination="${install_directory}/${binary_name}"
releases_url="https://api.github.com/repos/${repository}/releases?per_page=100"
curl_arguments=(-fsSL)
if [[ -n "${GH_TOKEN:-}" ]]; then
  curl_arguments+=(-H "Authorization: Bearer ${GH_TOKEN}")
fi

release_json=$(curl "${curl_arguments[@]}" "$releases_url")
release_tag=$(
  printf '%s\n' "$release_json" |
    awk '
      /"tag_name":[[:space:]]*"artifact-v[^"]+"/ {
        tag = $0
        sub(/^.*"tag_name":[[:space:]]*"/, "", tag)
        sub(/".*$/, "", tag)
        draft = 0
      }
      /"draft":[[:space:]]*true/ { draft = 1 }
      /"prerelease":[[:space:]]*false/ && tag != "" && draft == 0 { print tag; exit }
    '
)
if [[ -z "$release_tag" ]]; then
  echo "No published artifact CLI release was found"
  exit 1
fi
asset_api_url=$(
  printf '%s\n' "$release_json" |
    awk -v asset="$asset" '
      /"url":[[:space:]]*"https:\/\/api.github.com\/repos\/[^"]+\/releases\/assets\/[0-9]+"/ {
        url = $0
        sub(/^.*"url":[[:space:]]*"/, "", url)
        sub(/".*$/, "", url)
      }
      /"name":[[:space:]]*"/ {
        name = $0
        sub(/^.*"name":[[:space:]]*"/, "", name)
        sub(/".*$/, "", name)
        if (name == asset) { print url; exit }
      }
    '
)
download_url="https://github.com/${repository}/releases/download/${release_tag}/${asset}"

echo "Detected: ${operating_system}-${architecture}"
echo "Installing to: ${destination}"
mkdir -p "$install_directory"
temporary_file=$(mktemp "${install_directory}/.${binary_name}.XXXXXX")
trap 'rm -f "$temporary_file"' EXIT
echo "Downloading from: ${download_url}"
if [[ -n "${GH_TOKEN:-}" && -n "$asset_api_url" ]]; then
  curl "${curl_arguments[@]}" -H "Accept: application/octet-stream" "$asset_api_url" -o "$temporary_file"
else
  curl "${curl_arguments[@]}" "$download_url" -o "$temporary_file"
fi
chmod +x "$temporary_file"
mv -f "$temporary_file" "$destination"
trap - EXIT
echo "Installed ${binary_name} to ${destination}"

if [[ ":$PATH:" != *":${install_directory}:"* ]]; then
  echo ""
  echo "Add this to your shell profile to use artifact:"
  echo '  export PATH="$HOME/.local/bin:$PATH"'
fi
