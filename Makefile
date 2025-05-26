OIDC_DIR         := $(CURDIR)/test/oidc-e2e
DEX_SSL_DIR      := $(OIDC_DIR)/ssl
KIND_CLUSTER     ?= oidc-e2e
TESTBUILD_DIR    := $(CURDIR)/.testbuild
DEX_IMAGE        := ghcr.io/dexidp/dex:v2.42.1
TERRAFORM_VERSION := 1.12.1
PROVIDER_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.1.0")

# 	  TF_ACC_TERRAFORM_VERSION=$(TERRAFORM_VERSION) \


.PHONY: oidc-setup test-acc build vet clean test install docs

build:
	@echo "🔨 Building provider binary"
	go build -o bin/terraform-provider-k8sinline .

test:
	@echo "🧪 Running unit tests"
	go test -v ./... -run "^Test[^A].*"

install:
	@echo "🔧 Detecting system and building provider for installation..."
	@# Detect OS and architecture
	@OS=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
	ARCH=$$(uname -m); \
	case $$ARCH in \
		x86_64|amd64) GOARCH=amd64 ;; \
		arm64|aarch64) GOARCH=arm64 ;; \
		armv7l) GOARCH=arm ;; \
		i386|i686) GOARCH=386 ;; \
		*) echo "❌ Unsupported architecture: $$ARCH"; exit 1 ;; \
	esac; \
	case $$OS in \
		darwin) GOOS=darwin ;; \
		linux) GOOS=linux ;; \
		mingw*|msys*|cygwin*) GOOS=windows ;; \
		*) echo "❌ Unsupported OS: $$OS"; exit 1 ;; \
	esac; \
	PROVIDER_NAME=terraform-provider-k8sinline; \
	VERSION=$(PROVIDER_VERSION); \
	BINARY_NAME=$${PROVIDER_NAME}_$${VERSION}_$${GOOS}_$${GOARCH}; \
	if [ "$$GOOS" = "windows" ]; then \
		BINARY_NAME=$${BINARY_NAME}.exe; \
		FINAL_BINARY=$${PROVIDER_NAME}.exe; \
	else \
		FINAL_BINARY=$$PROVIDER_NAME; \
	fi; \
	INSTALL_DIR=$$HOME/.terraform.d/plugins/registry.terraform.io/local/k8sinline/$$VERSION/$${GOOS}_$${GOARCH}; \
	echo "🏗️  Building $$BINARY_NAME for $$GOOS/$$GOARCH (version $$VERSION)..."; \
	mkdir -p bin; \
	GOOS=$$GOOS GOARCH=$$GOARCH CGO_ENABLED=0 go build -ldflags="-w -s" -o bin/$$BINARY_NAME .; \
	echo "📦 Installing to $$INSTALL_DIR..."; \
	mkdir -p $$INSTALL_DIR; \
	cp bin/$$BINARY_NAME $$INSTALL_DIR/$$FINAL_BINARY; \
	chmod +x $$INSTALL_DIR/$$FINAL_BINARY; \
	echo "✅ Provider installed successfully!"; \
	echo ""; \
	echo "📍 Installed at: $$INSTALL_DIR/$$FINAL_BINARY"; \
	echo "🏷️  Version: $$VERSION"; \
	echo "💻 Platform: $$GOOS/$$GOARCH"; \
	echo ""; \
	echo "📝 To use this provider, add to your Terraform configuration:"; \
	echo ""; \
	echo "terraform {"; \
	echo "  required_providers {"; \
	echo "    k8sinline = {"; \
	echo "      source  = \"local/k8sinline\""; \
	echo "      version = \"$$VERSION\""; \
	echo "    }"; \
	echo "  }"; \
	echo "}"; \
	echo ""; \
	echo "provider \"k8sinline\" {}"; \
	echo ""; \
	echo "Then run: terraform init"

oidc-setup:
	@echo "🔐 Generating self‑signed certs"
	@rm -fr $(DEX_SSL_DIR)
	@mkdir -p $(DEX_SSL_DIR)
	@cd $(OIDC_DIR) && ./gencert.sh

	@echo "🧹 Cleaning old Dex container"
	- docker rm -f dex || true

	@echo "🚀 Starting Dex (HTTPS)"
	@docker run -d --name dex --network kind \
	  -v $(OIDC_DIR)/dex-config.yaml:/etc/dex/config.yaml \
	  -v $(DEX_SSL_DIR)/cert.pem:/etc/dex/tls.crt \
	  -v $(DEX_SSL_DIR)/key.pem:/etc/dex/tls.key \
	  -p 5556:5556 \
	  $(DEX_IMAGE) \
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

test-acc: oidc-setup
	@echo "🏃 Running acceptance tests..."; \
	export \
	  TF_ACC=1 \
	  TF_ACC_TERRAFORM_PATH="$(shell which terraform)" \
	  TF_ACC_K8S_HOST="$$(cat $(TESTBUILD_DIR)/cluster-endpoint.txt)" \
	  TF_ACC_K8S_CA="$$(base64 < $(TESTBUILD_DIR)/mock-ca.crt | tr -d '\n')" \
	  TF_ACC_K8S_CMD="$(OIDC_DIR)/get-token.sh" \
	  TF_ACC_KUBECONFIG_RAW="$$(cat $(TESTBUILD_DIR)/kubeconfig.yaml)"; \
	echo "TF_ACC_K8S_HOST=$$TF_ACC_K8S_HOST"; \
	echo "TF_ACC_K8S_CA=$$(echo $$TF_ACC_K8S_CA | cut -c1-20)..."; \
	echo "TF_ACC_K8S_CMD=$$TF_ACC_K8S_CMD"; \
	echo "TF_ACC_KUBECONFIG_RAW=$$(echo $$TF_ACC_KUBECONFIG_RAW | cut -c1-20)..."; \
	go test -cover -v ./internal/k8sinline/... -timeout 30m -run "TestAcc"

clean:
	-docker rm -f dex
	-kind delete cluster --name $(KIND_CLUSTER)
	-rm -rf $(TESTBUILD_DIR) $(DEX_SSL_DIR)
	-rm -rf bin/

vet:
	@echo "🔍 Running go vet on all packages"
	@go vet ./...

docs:
	tfplugindocs

