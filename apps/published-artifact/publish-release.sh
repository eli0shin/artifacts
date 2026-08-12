#!/usr/bin/env bash
set -euo pipefail

repository=${GITHUB_REPOSITORY:-eli0shin/proxmox-config}
version=$(node -p "require('./apps/published-artifact/package.json').version")
tag="artifact-v${version}"

if gh release view "$tag" --repo "$repository" >/dev/null 2>&1; then
  is_draft=$(gh release view "$tag" --repo "$repository" --json isDraft --jq '.isDraft')
  if [[ "$is_draft" == "false" ]]; then
    exit 0
  fi
else
  gh release create "$tag" \
    --repo "$repository" \
    --target "${GITHUB_SHA:?GITHUB_SHA is required}" \
    --title "artifact ${version}" \
    --generate-notes \
    --draft
fi

printf 'New tag: artifact@%s\n' "$version"
