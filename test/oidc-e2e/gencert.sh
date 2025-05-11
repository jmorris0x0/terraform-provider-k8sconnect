#!/bin/bash
set -euo pipefail

# create ssl directory
mkdir -p ssl

# generate OpenSSL config for SAN
cat <<EOF >ssl/req.cnf
[req]
req_extensions = v3_req
distinguished_name = req_distinguished_name

[req_distinguished_name]

[v3_req]
basicConstraints = CA:FALSE
keyUsage = nonRepudiation, digitalSignature, keyEncipherment
subjectAltName = @alt_names

[alt_names]
DNS.1 = dex.example.com
EOF

# generate CA key and self‑signed CA cert
openssl genrsa -out ssl/ca-key.pem 2048
openssl req -x509 -new -nodes \
  -key ssl/ca-key.pem \
  -days 10 \
  -out ssl/ca.pem \
  -subj "/CN=kube-ca"

# generate server key and CSR
openssl genrsa -out ssl/key.pem 2048
openssl req -new \
  -key ssl/key.pem \
  -out ssl/csr.pem \
  -subj "/CN=kube-ca" \
  -config ssl/req.cnf

# sign the server CSR with our CA
openssl x509 -req \
  -in ssl/csr.pem \
  -CA ssl/ca.pem \
  -CAkey ssl/ca-key.pem \
  -CAcreateserial \
  -out ssl/cert.pem \
  -days 10 \
  -extensions v3_req \
  -extfile ssl/req.cnf
