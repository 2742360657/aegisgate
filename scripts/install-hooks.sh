#!/usr/bin/env bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
configured="$(git config --get core.hooksPath || true)"
if [[ -n "$configured" && "$configured" != ".githooks" ]]; then
  echo "Existing hooksPath detected; integrate the checks manually instead of replacing it." >&2
  exit 1
fi
if [[ -z "$configured" ]]; then
  for hook in pre-commit pre-push; do
    if [[ -e "$(git rev-parse --git-path "hooks/$hook")" ]]; then
      echo "Existing $hook hook detected; integrate manually instead of replacing it." >&2
      exit 1
    fi
  done
fi
chmod +x .githooks/pre-commit .githooks/pre-push
git config --local core.hooksPath .githooks
echo "Installed repository-local staged-content and pre-push history checks."
