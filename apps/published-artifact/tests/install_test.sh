#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)
temporary_directory=$(mktemp -d)
trap 'rm -rf "$temporary_directory"' EXIT
mkdir -p "$temporary_directory/bin" "$temporary_directory/home"

cat >"$temporary_directory/bin/uname" <<'MOCK'
#!/usr/bin/env bash
case "${1:-}" in
  -s) printf 'Linux\n' ;;
  -m) printf 'x86_64\n' ;;
  *) exit 2 ;;
esac
MOCK
cat >"$temporary_directory/bin/curl" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
url=""
destination=""
while (($#)); do
  case "$1" in
    -o)
      destination=$2
      shift 2
      ;;
    http*)
      url=$1
      shift
      ;;
    *) shift ;;
  esac
done
if [[ "$url" == https://api.github.com/* ]]; then
  cat <<'JSON'
[
  {
    "tag_name": "artifact-v1.2.3",
    "draft": false,
    "prerelease": false
  }
]
JSON
  exit 0
fi
printf '%s\n' "$url" >"$CURL_URL_LOG"
printf '#!/usr/bin/env bash\nprintf "artifact installed\\n"\n' >"$destination"
MOCK
chmod +x "$temporary_directory/bin/uname" "$temporary_directory/bin/curl"

output=$(HOME="$temporary_directory/home" \
  PATH="$temporary_directory/bin:/usr/bin:/bin" \
  CURL_URL_LOG="$temporary_directory/curl-url" \
  bash "$repo_root/apps/published-artifact/install.sh")

expected=$(cat <<EOF
Detected: linux-x64
Installing to: $temporary_directory/home/.local/bin/artifact
Downloading from: https://github.com/eli0shin/artifacts/releases/download/artifact-v1.2.3/artifact-linux-x64
Installed artifact to $temporary_directory/home/.local/bin/artifact

Add this to your shell profile to use artifact:
  export PATH="\$HOME/.local/bin:\$PATH"
EOF
)
[[ "$output" == "$expected" ]] || {
  printf 'unexpected installer output:\n%s\n' "$output" >&2
  exit 1
}
[[ $(<"$temporary_directory/curl-url") == "https://github.com/eli0shin/artifacts/releases/download/artifact-v1.2.3/artifact-linux-x64" ]]
[[ -x "$temporary_directory/home/.local/bin/artifact" ]]
[[ $("$temporary_directory/home/.local/bin/artifact") == "artifact installed" ]]

printf '#!/usr/bin/env bash\nprintf "existing artifact\\n"\n' >"$temporary_directory/home/.local/bin/artifact"
chmod +x "$temporary_directory/home/.local/bin/artifact"
cat >"$temporary_directory/bin/curl" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == *"api.github.com"* ]]; then
  printf '[{"tag_name":"artifact-v1.2.3","draft":false,"prerelease":false}]\n'
  exit 0
fi
while (($#)); do
  if [[ "$1" == "-o" ]]; then
    printf 'partial download' >"$2"
    exit 22
  fi
  shift
done
exit 22
MOCK
chmod +x "$temporary_directory/bin/curl"
if HOME="$temporary_directory/home" \
  PATH="$temporary_directory/bin:/usr/bin:/bin" \
  bash "$repo_root/apps/published-artifact/install.sh" >/dev/null 2>&1; then
  echo "failed installer unexpectedly succeeded" >&2
  exit 1
fi
[[ $("$temporary_directory/home/.local/bin/artifact") == "existing artifact" ]]
[[ $(find "$temporary_directory/home/.local/bin" -maxdepth 1 -name '.artifact.*' -print -quit) == "" ]]

printf 'Artifact installer contract passed\n'
