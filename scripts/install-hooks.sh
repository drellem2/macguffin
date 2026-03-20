#!/bin/sh
# Install git hooks for local development.
set -e

repo_root="$(git rev-parse --show-toplevel)"
hook_dir="$repo_root/.git/hooks"

# .git might be a file (worktree) — resolve the actual hooks dir
if [ -f "$repo_root/.git" ]; then
    gitdir="$(sed 's/^gitdir: //' "$repo_root/.git")"
    hook_dir="$gitdir/hooks"
fi

mkdir -p "$hook_dir"
cp "$repo_root/scripts/pre-commit" "$hook_dir/pre-commit"
chmod +x "$hook_dir/pre-commit"

echo "Hooks installed to $hook_dir"
