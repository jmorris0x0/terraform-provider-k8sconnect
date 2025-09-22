#!/usr/bin/env bash

DEX_URL="https://127.0.0.1:5556/dex"
CLIENT_ID="kubernetes"
CLIENT_SECRET="ZXhhbXBsZS1hcHAtc2VjcmV0"
USERNAME="admin@example.com"
PASSWORD="password"

# Get token expiry from env var, default to 5 minutes
TOKEN_EXPIRY_SECONDS="${TOKEN_EXPIRY_SECONDS:-300}"

echo "PLUGIN CALLED at $(date -u +'%Y-%m-%dT%H:%M:%SZ')" >>/tmp/kubectl-exec.log
echo "Token will expire in ${TOKEN_EXPIRY_SECONDS} seconds" >>/tmp/kubectl-exec.log

# Request token using password grant
RESPONSE="$(curl -s --insecure -X POST "${DEX_URL}/token" \
  -d grant_type=password \
  -d username="$USERNAME" \
  -d password="$PASSWORD" \
  -d client_id="$CLIENT_ID" \
  -d client_secret="$CLIENT_SECRET" \
  -d scope='openid email profile')"

TOKEN="$(echo "$RESPONSE" | jq -r '.id_token // empty' | tr -d '\n')"

if [[ -z $TOKEN || $TOKEN == "null" ]]; then
  echo "❌ Failed to get token" >>/tmp/kubectl-exec.log
  exit 1
fi

# Calculate expiry based on environment variable
EXPIRY=$(date -u -d "+${TOKEN_EXPIRY_SECONDS} seconds" +'%Y-%m-%dT%H:%M:%SZ' 2>/dev/null ||
  date -u -v "+${TOKEN_EXPIRY_SECONDS}S" +'%Y-%m-%dT%H:%M:%SZ')

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
