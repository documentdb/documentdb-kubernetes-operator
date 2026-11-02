#!/usr/bin/env bash
set -euo pipefail

RAW_VERSION="${RAW_VERSION:?DocumentDB version is required}"
PACKAGE_SOURCE=apt
SOURCE_RELEASE_TAG=""
REQUESTED_SOURCE="${DOCUMENTDB_PACKAGE_SOURCE:-auto}"
case "$REQUESTED_SOURCE" in
  auto|apt|source) ;;
  *)
    echo "DOCUMENTDB_PACKAGE_SOURCE must be auto, apt or source, got: $REQUESTED_SOURCE" >&2
    exit 1
    ;;
esac

if [[ "$RAW_VERSION" =~ ^[vV]?([0-9]+)\.([0-9]+)(\.([0-9]+))?[-~][rR][cC]([1-9][0-9]*)$ ]]; then
  RELEASE_BASE="${BASH_REMATCH[1]}.${BASH_REMATCH[2]}"
  PATCH_VERSION="${BASH_REMATCH[4]:-0}"
  RC_NUMBER="${BASH_REMATCH[5]}"
  VERSION_DASH="${RELEASE_BASE}-${PATCH_VERSION}"
  if [[ "$PATCH_VERSION" != 0 ]]; then
    RELEASE_BASE="${RELEASE_BASE}.${PATCH_VERSION}"
  fi
  VERSION="${RELEASE_BASE}-rc${RC_NUMBER}"
  EXPECTED_DEB_VERSION="${RELEASE_BASE}~rc${RC_NUMBER}"
  SOURCE_RELEASE_TAG="v${RELEASE_BASE}-RC${RC_NUMBER}"
  PACKAGE_SOURCE=source
  if [[ "$REQUESTED_SOURCE" == apt ]]; then
    echo "Release candidates build from source; DOCUMENTDB_PACKAGE_SOURCE=apt is not supported for $RAW_VERSION" >&2
    exit 1
  fi
else
  VERSION="$RAW_VERSION"
  if [[ "$VERSION" =~ ^[0-9]+\.[0-9]+-[0-9]+$ ]]; then
    VERSION="${VERSION/-/.}"
  fi
  if [[ ! "$VERSION" =~ ^([0-9]+\.[0-9]+)\.([0-9]+)$ ]]; then
    echo "Version must be X.Y.Z or an RC such as 1.0-RC2, got: $RAW_VERSION" >&2
    exit 1
  fi
  VERSION_DASH="${BASH_REMATCH[1]}-${BASH_REMATCH[2]}"
  EXPECTED_DEB_VERSION="$VERSION_DASH"
  # Stable releases come from PGDG unless forced to source, e.g. when PGDG
  # has not yet packaged a release that is already tagged upstream.
  if [[ "$REQUESTED_SOURCE" == source ]]; then
    PACKAGE_SOURCE=source
    SOURCE_RELEASE_TAG="v${VERSION_DASH}"
  fi
fi

SHORT_SHA="${GITHUB_SHA:0:7}"
IMAGE_TAG="${VERSION}-build-${GITHUB_RUN_ID}-${GITHUB_RUN_ATTEMPT}-${SHORT_SHA}"
GATEWAY_SOURCE_IMAGE="${DOCUMENTDB_GATEWAY_IMAGE_REPO}:pg17-${VERSION}"
printf '%s\n' \
  "documentdb_version=$VERSION" \
  "documentdb_version_dash=$VERSION_DASH" \
  "expected_deb_version=$EXPECTED_DEB_VERSION" \
  "package_source=$PACKAGE_SOURCE" \
  "source_release_tag=$SOURCE_RELEASE_TAG" \
  "image_tag=$IMAGE_TAG" \
  "gateway_source_image=$GATEWAY_SOURCE_IMAGE"