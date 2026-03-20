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

# Requirements
- Runs on POSIX-y machines like BSD/Mac/Linux. If supporting a certain architecture will require much additional complexity this should be discussed and acknowledge in the architecture documentation before continuing.
