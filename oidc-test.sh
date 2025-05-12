#!/usr/bin/env bash
# grab the access_token from your get-token.sh output
ACCESS_TOKEN="$(./test/oidc-e2e/get-token.sh | jq -r '.access_token')"
CLUSTER_ENDPOINT="https://127.0.0.1:54090"

# 1) Using kubectl
kubectl get namespaces \
  --server="$CLUSTER_ENDPOINT" \
  --token="$ACCESS_TOKEN" \
  --insecure-skip-tls-verify

kubectl auth can-i --list \
  --server="$CLUSTER_ENDPOINT" \
  --token="$ACCESS_TOKEN" \
  --insecure-skip-tls-verify
