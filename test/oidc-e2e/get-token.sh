#!/usr/bin/env bash
set -euo pipefail

DEX_URL="https://127.0.0.1:5556/dex"
CLIENT_ID="kubernetes"
CLIENT_SECRET="client_test_secret"
USERNAME="admin"
PASSWORD="password"

echo "PLUGIN CALLED at $(date -u +'%Y-%m-%dT%H:%M:%SZ')" >>/tmp/kubectl-exec.log
echo "$KUBERNETES_EXEC_INFO" | jq . >/tmp/kubectl-exec.json 2>/dev/null || true

# Request token using password grant
RESPONSE="$(curl -s -X POST "${DEX_URL}/token" \
  -d grant_type=password \
  -d username="$USERNAME" \
  -d password="$PASSWORD" \
  -d client_id="$CLIENT_ID" \
  -d client_secret="$CLIENT_SECRET" \
  -d scope='openid email name')"

TOKEN="$(echo "$RESPONSE" | jq -r '.id_token // empty' | tr -d '\n')"

if [[ -z $TOKEN || $TOKEN == "null" ]]; then
  echo "❌ Failed to get token" >>/tmp/kubectl-exec.log
  exit 1
fi

echo "TOKEN_FOR_KUBECTL = '${TOKEN}'" >>/tmp/kubectl-exec.log

EXPIRY=$(date -u -d '+5 minutes' +'%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -v +5M +'%Y-%m-%dT%H:%M:%SZ')

jq -n --arg token "$TOKEN" --arg expiry "$EXPIRY" '
{
  apiVersion: "client.authentication.k8s.io/v1",
  kind: "ExecCredential",
  spec: {},
  status: {
    token: $token,
    expirationTimestamp: $expiry
  }
}
'
