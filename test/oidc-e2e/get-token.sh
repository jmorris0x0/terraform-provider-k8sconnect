#!/usr/bin/env bash

DEX_URL="https://127.0.0.1:5556/dex"
CLIENT_ID="kubernetes"
CLIENT_SECRET="ZXhhbXBsZS1hcHAtc2VjcmV0"
USERNAME="admin@example.com"
PASSWORD="password"

echo "PLUGIN CALLED at $(date -u +'%Y-%m-%dT%H:%M:%SZ')" >>/tmp/kubectl-exec.log
echo "$KUBERNETES_EXEC_INFO" | jq . >/tmp/kubectl-exec.json 2>/dev/null || true

#set -euo pipefail

# Request token using password grant
RESPONSE="$(curl -s --insecure -X POST "${DEX_URL}/token" \
  -d grant_type=password \
  -d username="$USERNAME" \
  -d password="$PASSWORD" \
  -d client_id="$CLIENT_ID" \
  -d client_secret="$CLIENT_SECRET" \
  -d scope='openid email profile')"

#echo $RESPONSE | jq

#echo

#for field in access_token id_token; do
#  TOKEN=$(echo "$RESPONSE" | jq -r --arg f "$field" '.[$f] // empty')
#  if [ -n "$TOKEN" ]; then
#    echo
#    echo "=== decoded $field header ==="
#    echo "$TOKEN" | cut -d. -f1 | base64 --decode | jq .
#    echo "=== decoded $field payload ==="
#    echo "$TOKEN" | cut -d. -f2 | base64 --decode | jq .
#  fi
#done

TOKEN="$(echo "$RESPONSE" | jq -r '.id_token // empty' | tr -d '\n')"

if [[ -z $TOKEN || $TOKEN == "null" ]]; then
  echo "❌ Failed to get token" >>/tmp/kubectl-exec.log
  exit 1
fi

# echo "TOKEN_FOR_KUBECTL = '${TOKEN}'" >>/tmp/kubectl-exec.log

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
