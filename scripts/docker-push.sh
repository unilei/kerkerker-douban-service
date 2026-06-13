#!/usr/bin/env bash

# Build and publish the service image to GitHub Container Registry.
# Usage: GITHUB_TOKEN=... ./scripts/docker-push.sh [version]

set -euo pipefail

REGISTRY="${REGISTRY:-ghcr.io}"
GITHUB_USERNAME="${GITHUB_USERNAME:-unilei}"
IMAGE_NAME="${IMAGE_NAME:-kerkerker-douban-service}"
VERSION="${1:-latest}"
FULL_IMAGE_NAME="${REGISTRY}/${GITHUB_USERNAME}/${IMAGE_NAME}"

if [ -z "${GITHUB_TOKEN:-}" ]; then
    echo "GITHUB_TOKEN is required and must include write:packages permission." >&2
    exit 1
fi

if ! docker info >/dev/null 2>&1; then
    echo "Docker is not running." >&2
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_DIR"

printf '%s' "$GITHUB_TOKEN" | docker login "$REGISTRY" -u "$GITHUB_USERNAME" --password-stdin

if ! docker buildx inspect multiarch >/dev/null 2>&1; then
    docker buildx create --name multiarch --use
else
    docker buildx use multiarch
fi

tags=(-t "${FULL_IMAGE_NAME}:${VERSION}")
if [ "$VERSION" != "latest" ]; then
    tags+=(-t "${FULL_IMAGE_NAME}:latest")
fi

docker buildx build \
    --platform linux/amd64,linux/arm64 \
    "${tags[@]}" \
    --push \
    .

echo "Published ${FULL_IMAGE_NAME}:${VERSION}"
