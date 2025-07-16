OIDC_DIR          := $(CURDIR)/test/oidc-e2e
DEX_SSL_DIR       := $(OIDC_DIR)/ssl
KIND_CLUSTER      ?= oidc-e2e
TESTBUILD_DIR     := $(CURDIR)/.testbuild
DEX_IMAGE         := ghcr.io/dexidp/dex:v2.42.1
TERRAFORM_VERSION := 1.13.0-alpha20250702
PROVIDER_VERSION  ?= 0.1.0

# Build variables for version injection
LDFLAGS := -ldflags="-w -s -X github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline.version=$(PROVIDER_VERSION)"

.PHONY: build
build:
	@echo "🔨 Building provider binary"
	go mod tidy
	go build $(LDFLAGS) -o bin/terraform-provider-k8sinline .

.PHONY: test
test:
	@echo "🧪 Running unit tests"
	go test -v ./... -run "^Test[^A].*"

.PHONY: install
install:
	@echo "🔧 Building and installing provider..."
	@echo "📦 Ensuring dependencies are up to date..."
	@go mod tidy
	@# Detect platform
	@OS=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
	ARCH=$$(uname -m); \
	case $$ARCH in \
		x86_64|amd64) TARGET_ARCH=amd64 ;; \
		arm64|aarch64) TARGET_ARCH=arm64 ;; \
		armv7l) TARGET_ARCH=arm ;; \
		i386|i686) TARGET_ARCH=386 ;; \
		*) echo "❌ Unsupported architecture: $$ARCH"; exit 1 ;; \
	esac; \
	case $$OS in \
		darwin) TARGET_OS=darwin ;; \
		linux) TARGET_OS=linux ;; \
		mingw*|msys*|cygwin*) TARGET_OS=windows ;; \
		*) echo "❌ Unsupported OS: $$OS"; exit 1 ;; \
	esac; \
	BINARY_NAME=terraform-provider-k8sinline_$(PROVIDER_VERSION)_$${TARGET_OS}_$${TARGET_ARCH}; \
	if [ "$$TARGET_OS" = "windows" ]; then \
		BINARY_NAME=$${BINARY_NAME}.exe; \
		FINAL_BINARY=terraform-provider-k8sinline.exe; \
	else \
		FINAL_BINARY=terraform-provider-k8sinline; \
	fi; \
	INSTALL_DIR=$$HOME/.terraform.d/plugins/registry.terraform.io/local/k8sinline/$(PROVIDER_VERSION)/$${TARGET_OS}_$${TARGET_ARCH}; \
	echo "🏗️  Building for $${TARGET_OS}/$${TARGET_ARCH}..."; \
	mkdir -p bin; \
	if ! GOOS=$$TARGET_OS GOARCH=$$TARGET_ARCH CGO_ENABLED=0 go build $(LDFLAGS) -o bin/$$BINARY_NAME .; then \
		echo "❌ Build failed!"; \
		exit 1; \
	fi; \
	echo "📦 Installing to $$INSTALL_DIR..."; \
	mkdir -p $$INSTALL_DIR; \
	cp bin/$$BINARY_NAME $$INSTALL_DIR/$$FINAL_BINARY; \
	chmod +x $$INSTALL_DIR/$$FINAL_BINARY; \
	echo "✅ Provider installed successfully!"; \
	echo ""; \
	echo "📍 Installed at: $$INSTALL_DIR/$$FINAL_BINARY"; \
	echo "🏷️  Version: $(PROVIDER_VERSION)"; \
	echo "💻 Platform: $${TARGET_OS}/$${TARGET_ARCH}"; \
	echo ""; \
	echo "Usage:"; \
	echo "  terraform {"; \
	echo "    required_providers {"; \
	echo "      k8sinline = {"; \
	echo "        source  = \"local/k8sinline\""; \
	echo "        version = \"$(PROVIDER_VERSION)\""; \
	echo "      }"; \
	echo "    }"; \
	echo "  }"

.PHONY: oidc-setup 
oidc-setup:
	@echo "🔐 Generating self‑signed certs"
	@rm -fr $(DEX_SSL_DIR)
	@mkdir -p $(DEX_SSL_DIR)
	@cd $(OIDC_DIR) && ./gencert.sh

	@echo "🌐 Ensuring 'kind' Docker network exists"
	- docker network inspect kind >/dev/null 2>&1 || docker network create kind

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

	@echo "🔐 Applying RBAC resources"
	kubectl apply -f $(OIDC_DIR)/rbac.yaml
	kubectl apply -f $(OIDC_DIR)/auth-resources.yaml

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

	@echo "🔑 Creating service account token"
	kubectl create token test-sa -n default --duration=24h > $(TESTBUILD_DIR)/sa-token.txt

	@echo "📜 Generating client certificates"
	@chmod +x $(OIDC_DIR)/setup-certs.sh
	@$(OIDC_DIR)/setup-certs.sh $(TESTBUILD_DIR)

.PHONY: testacc
testacc: oidc-setup
	@echo "🏃 Running acceptance tests..."; \
	TF_VERSION="$${TF_ACC_TERRAFORM_VERSION:-$(TERRAFORM_VERSION)}"; \
	if [ "$$TF_VERSION" != "$$(tfenv version-name)" ]; then \
		echo "Installing Terraform $$TF_VERSION via tfenv..."; \
		tfenv install $$TF_VERSION || true; \
		tfenv use $$TF_VERSION || true; \
	fi; \
	export \
	  TF_ACC=1 \
	  TF_ACC_K8S_HOST="$$(cat $(TESTBUILD_DIR)/cluster-endpoint.txt)" \
	  TF_ACC_K8S_CA="$$(base64 < $(TESTBUILD_DIR)/mock-ca.crt | tr -d '\n')" \
	  TF_ACC_K8S_CMD="$(OIDC_DIR)/get-token.sh" \
	  TF_ACC_KUBECONFIG_RAW="$$(cat $(TESTBUILD_DIR)/kubeconfig.yaml)" \
	  TF_ACC_K8S_TOKEN="$$(cat $(TESTBUILD_DIR)/sa-token.txt)" \
	  TF_ACC_K8S_CLIENT_CERT="$$(base64 < $(TESTBUILD_DIR)/client.crt | tr -d '\n')" \
	  TF_ACC_K8S_CLIENT_KEY="$$(base64 < $(TESTBUILD_DIR)/client.key | tr -d '\n')"; \
	echo "TF_ACC_K8S_HOST=$$TF_ACC_K8S_HOST"; \
	echo "TF_ACC_K8S_CA=$$(echo $$TF_ACC_K8S_CA | cut -c1-20)..."; \
	echo "TF_ACC_K8S_CMD=$$TF_ACC_K8S_CMD"; \
	echo "TF_ACC_KUBECONFIG_RAW=$$(echo $$TF_ACC_KUBECONFIG_RAW | cut -c1-20)..."; \
	echo "TF_ACC_K8S_TOKEN=$$(echo $$TF_ACC_K8S_TOKEN | cut -c1-20)..."; \
	echo "TF_ACC_K8S_CLIENT_CERT=$$(echo $$TF_ACC_K8S_CLIENT_CERT | cut -c1-20)..."; \
	echo "TF_ACC_K8S_CLIENT_KEY=$$(echo $$TF_ACC_K8S_CLIENT_KEY | cut -c1-20)..."; \
	echo "Terraform version: $$(terraform version -json | jq -r .terraform_version)"; \
	go test -cover -v ./internal/k8sinline/... -timeout 30m -run "TestAcc"

.PHONY: clean
clean:
	-docker rm -f dex
	-kind delete cluster --name $(KIND_CLUSTER)
	-rm -rf $(TESTBUILD_DIR) $(DEX_SSL_DIR)
	-rm -rf bin/

.PHONY: vet
vet:
	@echo "🔍 Running go vet on all packages"
	@go vet ./...

.PHONY: docs
docs:
	tfplugindocs

.PHONY: lint
lint: vet
	@echo "🔍 Running golangci-lint..."
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "Installing golangci-lint..."; \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | \
			sh -s -- -b $$(go env GOPATH)/bin; \
	fi
	golangci-lint run --timeout=5m

.PHONY: security-scan
security-scan:
	@echo "🔒 Running security scans..."
	@if ! command -v gosec >/dev/null 2>&1; then \
		echo "Installing gosec..."; \
		go install github.com/securego/gosec/v2/cmd/gosec@latest; \
	fi
	@if ! command -v govulncheck >/dev/null 2>&1; then \
		echo "Installing govulncheck..."; \
		go install golang.org/x/vuln/cmd/govulncheck@latest; \
	fi
	gosec -quiet ./...
	govulncheck ./...

.PHONY: release-dry-run
release-dry-run:
	@echo "🔍 Testing release process locally..."
	@if ! command -v goreleaser >/dev/null 2>&1; then \
		echo "Installing goreleaser..."; \
		go install github.com/goreleaser/goreleaser/v2@latest; \
	fi
	goreleaser release --snapshot --skip=publish --clean

.PHONY: release-check
release-check:
	@echo "✅ Checking release configuration..."
	@if ! command -v goreleaser >/dev/null 2>&1; then \
		echo "Installing goreleaser..."; \
		go install github.com/goreleaser/goreleaser/v2@latest; \
	fi
	goreleaser check

.PHONY: coverage
coverage: oidc-setup
	@echo "📊 Building unified coverage report"
	@PROFILE=coverage.out ; \
	TF_ACC=1 \
	  TF_ACC_K8S_HOST="$$(cat $(TESTBUILD_DIR)/cluster-endpoint.txt)" \
	  TF_ACC_K8S_CA="$$(base64 < $(TESTBUILD_DIR)/mock-ca.crt | tr -d '\n')" \
	  TF_ACC_K8S_CMD="$(OIDC_DIR)/get-token.sh" \
	  TF_ACC_KUBECONFIG_RAW="$$(cat $(TESTBUILD_DIR)/kubeconfig.yaml)" \
	  TF_ACC_K8S_TOKEN="$$(cat $(TESTBUILD_DIR)/sa-token.txt)" \
	  TF_ACC_K8S_CLIENT_CERT="$$(base64 < $(TESTBUILD_DIR)/client.crt | tr -d '\n')" \
	  TF_ACC_K8S_CLIENT_KEY="$$(base64 < $(TESTBUILD_DIR)/client.key | tr -d '\n')" \
	  go test ./... -coverpkg=./... -covermode=atomic -coverprofile=$$PROFILE -count=1 ; \
	go tool cover -func=$$PROFILE ; \
	go tool cover -html=$$PROFILE -o coverage.html ; \
	echo "HTML report written to ./coverage.html"

