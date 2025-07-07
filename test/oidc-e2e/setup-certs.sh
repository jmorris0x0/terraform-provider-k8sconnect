#!/usr/bin/env bash
# test/oidc-e2e/setup-certs.sh
# Generate client certificates for testing client cert authentication

set -e

TESTBUILD_DIR="${1:-.testbuild}"

echo "📜 Generating client certificate and key..."
openssl genrsa -out "$TESTBUILD_DIR/client.key" 2048
openssl req -new -key "$TESTBUILD_DIR/client.key" \
  -out "$TESTBUILD_DIR/client.csr" \
  -subj "/CN=test-user/O=test-group"

echo "🔐 Creating Kubernetes CSR..."
CSR_NAME="test-user-csr-$(date +%s)"

# Create CSR manifest
cat >"$TESTBUILD_DIR/csr.yaml" <<EOF
apiVersion: certificates.k8s.io/v1
kind: CertificateSigningRequest
metadata:
  name: $CSR_NAME
spec:
  request: $(cat "$TESTBUILD_DIR/client.csr" | base64 | tr -d '\n')
  signerName: kubernetes.io/kube-apiserver-client
  usages:
  - client auth
EOF

# Apply and approve CSR
kubectl apply -f "$TESTBUILD_DIR/csr.yaml"
kubectl certificate approve "$CSR_NAME"

# Wait for certificate to be issued
echo "⏳ Waiting for certificate..."
for i in {1..30}; do
  if kubectl get csr "$CSR_NAME" -o jsonpath='{.status.certificate}' | grep -q .; then
    break
  fi
  sleep 1
done

# Extract certificate
kubectl get csr "$CSR_NAME" -o jsonpath='{.status.certificate}' | base64 -d >"$TESTBUILD_DIR/client.crt"

# Clean up CSR
kubectl delete csr "$CSR_NAME"

echo "✅ Client certificate generated successfully"
