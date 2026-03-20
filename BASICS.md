# Architecture Basics
- Golang.
- Github.
- Builds in Github Actions for CICD.
- Pre-hook commit at least does a basic build & format to ensure code quality.
- install.sh script pulls the latest binary and installs.
- comprehensive unit testing.

# Principles
- UNIX philosophy.
- Ergonomic for both humans and agents. Scales with agentic workloads.

# Dev Basics
- Unit tests for all new golang functionality.
- Shell script coverage of the CLI functionality.

# Testing

Two levels of testing are required for all new features:

**1. Go unit tests** (`go test ./...`)
- Live in `*_test.go` files next to the code they test (e.g. `internal/event/event_test.go`).
- Cover the internal library logic: parsing, filtering, edge cases.
- Run via `./test.sh` or `go test ./...`.

**2. Shell script E2E tests** (`scripts/*_test.sh`)
- Cover the CLI surface: flags, output format, error cases, integration between commands.
- Each test script follows the same pattern: `pass()`/`fail()` helpers, `clean()` to reset
  `~/.macguffin`, and a summary at the end that exits non-zero on any failure.
- Accept the binary path as `$1` (default `./mg`): `./scripts/event_test.sh ./mg`
- See `scripts/e2e_milestones_test.sh` and `scripts/event_test.sh` for examples.

When adding a new `mg` subcommand, add both unit tests for the internal package and a
shell script test in `scripts/` for the CLI behavior.

# Requirements
- Runs on POSIX-y machines like BSD/Mac/Linux. If supporting a certain architecture will require much additional complexity this should be discussed and acknowledge in the architecture documentation before continuing.
