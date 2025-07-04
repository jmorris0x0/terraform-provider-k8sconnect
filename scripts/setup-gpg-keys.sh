#!/bin/bash
# Setup GPG keys for Terraform Provider signing

set -e

echo "🔑 Setting up GPG keys for Terraform Provider signing..."
echo

# Check if GPG is installed
if ! command -v gpg &>/dev/null; then
  echo "❌ GPG is not installed. Please install it first:"
  echo "   macOS: brew install gnupg"
  echo "   Linux: sudo apt-get install gnupg"
  exit 1
fi

# Generate key
echo "Generating GPG key..."
cat >gpg-batch.txt <<EOF
%echo Generating GPG key for Terraform Provider signing
Key-Type: RSA
Key-Length: 4096
Subkey-Type: RSA
Subkey-Length: 4096
Name-Real: k8sinline Terraform Provider
Name-Email: terraform@k8sinline.local
Expire-Date: 0
%no-protection
%commit
%echo done
EOF

gpg --batch --generate-key gpg-batch.txt
rm gpg-batch.txt

# Get the key ID
KEY_ID=$(gpg --list-secret-keys --keyid-format=long terraform@k8sinline.local | grep sec | awk '{print $2}' | cut -d'/' -f2)
FINGERPRINT=$(gpg --list-secret-keys --keyid-format=long terraform@k8sinline.local | grep -A1 "sec" | tail -1 | tr -d ' ')

echo
echo "✅ Key generated successfully!"
echo "   Key ID: $KEY_ID"
echo "   Fingerprint: $FINGERPRINT"
echo

# Export keys
echo "Exporting keys..."
gpg --armor --export terraform@k8sinline.local >k8sinline-public.asc
gpg --armor --export-secret-keys terraform@k8sinline.local >k8sinline-private.asc

echo "📄 Keys exported to:"
echo "   Public key:  k8sinline-public.asc"
echo "   Private key: k8sinline-private.asc"
echo

echo "🔧 Next steps:"
echo "1. Add these secrets to your GitHub repository:"
echo "   - GPG_PRIVATE_KEY: Copy contents of k8sinline-private.asc"
echo "   - GPG_PASSPHRASE: (leave empty since we used --no-protection)"
echo
echo "2. When ready to publish to Terraform Registry:"
echo "   - Upload k8sinline-public.asc to https://registry.terraform.io/settings/gpg-keys"
echo
echo "3. Keep your private key safe!"
echo

echo "🚨 Security reminder: Delete the private key file after adding to GitHub:"
echo "   rm k8sinline-private.asc"
