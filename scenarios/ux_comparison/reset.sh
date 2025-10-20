#!/usr/bin/env bash
set -euo pipefail

# Quiet, idempotent cleanup
rm -f .terraform.lock.hcl
rm -f provider-comparison-config
rm -f terraform.*
rm -rf .terraform

# Delete the kind cluster if present. Do not error if it is missing.
if command -v kind >/dev/null 2>&1; then
  kind delete cluster --name provider-comparison >/dev/null 2>&1 || true
fi

echo "Remember to run make install if testing a new provider version"
