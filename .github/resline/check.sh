#!/usr/bin/env bash
# Pre-push checks for the resline fork: run from the repo root before pushing main.
# CI's golangci and resline.yml run the same checks; this catches them locally first.
set -euo pipefail

.github/resline/check-upstream-urls.sh
.github/resline/check-comment-length.py
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 fmt --diff ./...
echo "resline checks passed."
