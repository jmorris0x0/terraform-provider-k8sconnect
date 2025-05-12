OIDC_DIR    := test/oidc-e2e
DEX_SSL_DIR := $(OIDC_DIR)/ssl
KIND_CLUSTER ?= oidc-e2e




oidc-setup:
	@echo "🔐 Generating self‑signed certs"
	@rm -fr $(DEX_SSL_DIR)
	@mkdir -p $(DEX_SSL_DIR)
	@cd $(OIDC_DIR) && ./gencert.sh

	@echo "🧹 Cleaning old Dex container"
	- docker rm -f dex || true

	@echo "🚀 Starting Dex (HTTPS)"
	@docker run -d --name dex --network kind \
	  -v $(CURDIR)/$(OIDC_DIR)/dex-config.yaml:/etc/dex/config.yaml \
	  -v $(CURDIR)/$(DEX_SSL_DIR)/cert.pem:/etc/dex/tls.crt \
	  -v $(CURDIR)/$(DEX_SSL_DIR)/key.pem:/etc/dex/tls.key \
	  -p 5556:5556 \
	  ghcr.io/dexidp/dex:v2.42.1 \
	  dex serve /etc/dex/config.yaml

	@echo "🔎 Waiting for Dex to be ready"
	@until curl -sf --insecure https://localhost:5556/dex/.well-known/openid-configuration; do sleep 0.5; done
	@echo "✅ Dex is up!"

	@echo "🧹 Deleting existing Kind cluster (if any)"
	- kind delete cluster --name $(KIND_CLUSTER) || true

	@echo "🚀 Creating Kind cluster with OIDC config"
	kind create cluster --name $(KIND_CLUSTER) --config=$(OIDC_DIR)/kind-oidc.yaml

	@echo "🔐 Applying minimal RBAC for OIDC user"
	kubectl apply -f $(OIDC_DIR)/rbac.yaml

	@echo "📥 Extracting kubeconfig and CA for Terraform"
	mkdir -p $(TESTBUILD_DIR)
	kubectl config view --raw --minify \
	  --output=jsonpath='{.clusters[0].cluster.certificate-authority-data}' \
	  | base64 -d > $(TESTBUILD_DIR)/mock-ca.crt
	kubectl config view --raw --minify \
	  --output=jsonpath='{.clusters[0].cluster.server}' \
	  > $(TESTBUILD_DIR)/cluster-endpoint.txt
	kubectl config view --raw --minify \
	  > $(TESTBUILD_DIR)/kubeconfig.yaml


test-acc:
    export TF_ACC_K8S_HOST=... \
           TF_ACC_K8S_CA=... \
           TF_ACC_K8S_CMD=... \
           TF_ACC_KUBECONFIG_RAW="$$(cat kubeconfig.yaml)" \
    go test ./internal/k8sinline/... -timeout 30m -run TestAccManifestResource_Basic
