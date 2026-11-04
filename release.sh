#!/bin/sh
set -e

if [ -z "$1" ]; then
    echo "Usage: ./release.sh <version>" >&2
    echo "Example: ./release.sh 0.1.0" >&2
    exit 1
fi

version="v${1#v}"

if git rev-parse "$version" >/dev/null 2>&1; then
    echo "Error: tag $version already exists" >&2
    exit 1
fi

git tag "$version"
git push origin "$version"

echo "Tagged and pushed $version."
echo "release.yml may publish it if GitHub Actions runs; it is not required."
echo "Check:   gh release view $version --repo drellem2/macguffin --json assets -q '.assets[].name'"
echo "If no release appears, publish from a checkout of the tag:"
echo "         scripts/release-local.sh $version    (see docs/release-process.md)"
