# Dockerfile.mg -- publish ONLY the mg binary, for other images to COPY --from.
#
# This image is not runnable and is not meant to be. It is a scratch layer holding a
# single static /mg, so that a consumer takes the binary with one line:
#
#     COPY --from=ghcr.io/<owner>/macguffin@sha256:<index-digest> /mg /usr/local/bin/mg
#
# and needs no Go toolchain stage, no clone of this repo, and no credential of its
# own. The consumer that asked for this is the pi-agent image in payitgov/agents;
# its reasoning, and the requirement that the pin name the image INDEX digest rather
# than one platform's manifest, are recorded in that repo's
# docs/design/mg-artifact-delivery.md.
#
# Cross-compiled, never emulated: the builder stage is pinned to $BUILDPLATFORM and
# Go is told the target through $TARGETARCH, so linux/amd64 + linux/arm64 costs two
# native `go build`s instead of a QEMU-emulated toolchain. That is also why no
# setup-qemu step appears in the publishing workflow -- there is nothing to emulate.

ARG GO_VERSION=1.25

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS build

WORKDIR /src

# Dependencies first, so editing source does not re-download the module cache.
COPY go.mod go.sum ./
RUN go mod download

# Then the tree, .git INCLUDED -- the version is derived from the git tag at build
# time (there is no version constant to read; bc23153 deleted it deliberately). A
# context without .git, or one cloned with --depth 1, has no tags and cannot be
# stamped. Build from a full-history checkout.
COPY . .

ARG TARGETARCH

# The version string comes from build.sh's own derive_version, SOURCED rather than
# reimplemented: MG_BUILD_SKIP_MAIN exists precisely so that function can be reused
# without running the installer's main(). Copying the derivation here would give it a
# second home to drift from.
#
# Two hazards, both measured rather than assumed, which is why this is not a one-liner.
#
# 1. build.sh:5 runs `cd "$(dirname "$0")"`. Sourced from a Dockerfile RUN, $0 is
#    "/bin/sh", so that cd lands in /bin, which is not a git repo. Measured
#    2026-08-21: `/bin/sh -c '. ./build.sh; pwd'` prints /bin, while the same line
#    under `sh -c` prints the source directory -- so the failure appears ONLY in the
#    shape Docker actually uses, and a laptop check would not have found it.
#    Two things make this stage immune rather than merely repaired: the source runs
#    inside a command substitution, so its cd cannot move this RUN's own directory
#    away from the WORKDIR that `go build` needs; and derive_version is given an
#    ABSOLUTE path, so it reads the right repo no matter where the cd landed.
#
# 2. Deriving nothing is a normal outcome for a source build -- build.sh says so, and
#    falls back to an unstamped binary reporting `dev`. For a PUBLISHED artifact it is
#    not normal: an image whose /mg cannot say what it is defeats the point of pinning
#    it by digest. So this build fails closed, with the cause named.
RUN set -eu; \
    git config --global --add safe.directory /src; \
    version="$(export MG_BUILD_SKIP_MAIN=1; . /src/build.sh; derive_version /src)" || version=""; \
    if [ -z "$version" ]; then \
        echo "ERROR: no version could be derived from the build context." >&2; \
        echo "  A published mg must be identifiable; refusing to ship one reporting 'dev'." >&2; \
        echo "  Cause: no git metadata, or no vN.N.N tag reachable from HEAD." >&2; \
        echo "  Fix: build from a full-history checkout that includes tags." >&2; \
        exit 1; \
    fi; \
    commit="$(git rev-parse --short HEAD)"; \
    date="$(git log -1 --format=%cs)"; \
    echo "stamping mg ${version} (${commit}, ${date}) for linux/${TARGETARCH}"; \
    CGO_ENABLED=0 GOOS=linux GOARCH="${TARGETARCH}" go build -trimpath \
        -ldflags "-s -w -X main.version=${version} -X main.commit=${commit} -X main.date=${date}" \
        -o /out/mg ./cmd/mg

# Prove the thing about to be published actually runs, on the BUILDPLATFORM leg only:
# a cross-compiled arm64 binary cannot execute here, and this is not the place to
# reintroduce emulation. `mg --version` is the cheapest whole-binary smoke test --
# it builds the real cobra command tree and prints the stamp, so a binary that is
# unstamped, statically broken, or wrong-arch cannot pass quietly.
RUN if [ "$TARGETARCH" = "$(go env GOARCH)" ]; then \
        /out/mg --version; \
    else \
        echo "skipping run check: built linux/${TARGETARCH} on $(go env GOARCH), not executable here"; \
    fi

FROM scratch
COPY --from=build /out/mg /mg
