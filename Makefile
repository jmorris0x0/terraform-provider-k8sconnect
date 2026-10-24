OIDC_DIR          := $(CURDIR)/test/oidc-setup
DEX_SSL_DIR       := $(OIDC_DIR)/ssl
TESTBUILD_DIR     := $(CURDIR)/.testbuild
DEX_IMAGE         := ghcr.io/dexidp/dex:v2.42.1
TERRAFORM_VERSION := 1.17.0
K3S_IMAGE         ?= rancher/k3s:v1.37.1-k3s1
PROVIDER_VERSION  ?= 0.1.0

# Build variables for version injection
LDFLAGS := -ldflags="-w -s -X github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect.version=$(PROVIDER_VERSION)"

.PHONY: build
build:
	@echo "🔨 Building provider binary"
	go mod tidy
	go build $(LDFLAGS) -o bin/terraform-provider-k8sconnect .

.PHONY: test
test:
	@echo "🧪 Running unit tests"
	@go test -v $$(go list ./... | grep -v /test/examples | grep -v /test/doctest | grep -v /test/diagnostics) -run "^Test[^A].*"

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
	BINARY_NAME=terraform-provider-k8sconnect_$(PROVIDER_VERSION)_$${TARGET_OS}_$${TARGET_ARCH}; \
	if [ "$$TARGET_OS" = "windows" ]; then \
		BINARY_NAME=$${BINARY_NAME}.exe; \
		FINAL_BINARY=terraform-provider-k8sconnect.exe; \
	else \
		FINAL_BINARY=terraform-provider-k8sconnect; \
	fi; \
	INSTALL_DIR=$$HOME/.terraform.d/plugins/registry.terraform.io/local/k8sconnect/$(PROVIDER_VERSION)/$${TARGET_OS}_$${TARGET_ARCH}; \
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
	echo "      k8sconnect = {"; \
	echo "        source  = \"local/k8sconnect\""; \
	echo "        version = \"$(PROVIDER_VERSION)\""; \
	echo "      }"; \
	echo "    }"; \
	echo "  }"

.PHONY: create-cluster
create-cluster:
	@echo "🔍 Checking for k3d installation..."
	@if ! command -v k3d >/dev/null 2>&1; then \
		echo "❌ k3d is not installed!"; \
		echo ""; \
		echo "Please install k3d using one of these methods:"; \
		echo "  brew install k3d                    # macOS with Homebrew"; \
		echo "  curl -s https://raw.githubusercontent.com/k3d-io/k3d/main/install.sh | bash"; \
		echo ""; \
		echo "For more installation options, visit: https://k3d.io/stable/#installation"; \
		exit 1; \
	fi
	@echo "✅ k3d found: $$(k3d version)"
	@echo "🔐 Generating self‑signed certs"
	@rm -fr $(DEX_SSL_DIR)
	@mkdir -p $(DEX_SSL_DIR)
	@cd $(OIDC_DIR) && ./gencert.sh >/dev/null 2>&1

	@echo "🌐 Ensuring Docker network exists"
	- docker network inspect k3d-k8sconnect-test >/dev/null 2>&1 || docker network create k3d-k8sconnect-test

	@echo "🧹 Cleaning old Dex container"
	-@docker rm -f dex 2>/dev/null || true

	@echo "🚀 Starting Dex (HTTPS)"
	@docker run -d --name dex --network k3d-k8sconnect-test \
	  -v $(OIDC_DIR)/dex-config.yaml:/etc/dex/config.yaml \
	  -v $(DEX_SSL_DIR)/cert.pem:/etc/dex/tls.crt \
	  -v $(DEX_SSL_DIR)/key.pem:/etc/dex/tls.key \
	  -p 5556:5556 \
	  $(DEX_IMAGE) \
	  dex serve /etc/dex/config.yaml

	@echo "🔎 Waiting for Dex to be ready"
	@until curl -sf --insecure https://localhost:5556/dex/.well-known/openid-configuration; do sleep 0.5; done
	@echo "✅ Dex is up!"

	@echo "🧹 Deleting existing cluster (if any)"
	- k3d cluster delete k8sconnect-test || true

	@echo "🚀 Creating cluster with OIDC config"
	k3d cluster create \
	  --config=$(OIDC_DIR)/k3d-oidc.yaml \
	  --image $(K3S_IMAGE) \
	  --volume $(DEX_SSL_DIR)/cert.pem:/etc/kubernetes/pki/oidc/ca.pem@server:0

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
	@$(OIDC_DIR)/setup-certs.sh $(TESTBUILD_DIR) 2>/dev/null

.PHONY: testacc
testacc: create-cluster
	@echo "🏃 Running acceptance tests..."; \
	TF_VERSION="$${TF_ACC_TERRAFORM_VERSION:-$(TERRAFORM_VERSION)}"; \
	echo "Required Terraform version: $$TF_VERSION"; \
	if command -v tfenv >/dev/null 2>&1; then \
		echo "📦 tfenv detected - switching to Terraform $$TF_VERSION"; \
		if ! tfenv list 2>/dev/null | grep -q "$$TF_VERSION"; then \
			echo "   Installing Terraform $$TF_VERSION..."; \
			tfenv install $$TF_VERSION; \
		fi; \
		tfenv use $$TF_VERSION; \
	else \
		echo "ℹ️  tfenv not found - using system Terraform"; \
	fi; \
	ACTUAL_TF_VERSION=$$(terraform version -json 2>/dev/null | jq -r .terraform_version || terraform version | head -n1 | cut -d' ' -f2 | tr -d 'v'); \
	echo "Actual Terraform version: $$ACTUAL_TF_VERSION"; \
	if [ "$$ACTUAL_TF_VERSION" != "$$TF_VERSION" ]; then \
		echo "❌ ERROR: Terraform version mismatch!"; \
		echo "   Expected: $$TF_VERSION"; \
		echo "   Actual:   $$ACTUAL_TF_VERSION"; \
		echo "   Ensure Terraform $$TF_VERSION is installed"; \
		exit 1; \
	fi; \
	echo "✅ Terraform version verified"; \
	TEST_FILTER="$${TEST:-TestAcc}"; \
	echo "Running tests matching: $$TEST_FILTER"; \
	if [ -n "$$GITHUB_ACTIONS" ]; then \
		PARALLEL_FLAG="-parallel=4"; \
		echo "🚀 Running in GitHub Actions with parallelism=4"; \
	else \
		PARALLEL_FLAG=""; \
		echo "🚀 Running locally - using default parallelism"; \
	fi; \
	export \
	  TF_ACC=1 \
	  TF_ACC_TERRAFORM_PATH="$$(which terraform)" \
	  TF_ACC_K8S_HOST="$$(cat $(TESTBUILD_DIR)/cluster-endpoint.txt)" \
	  TF_ACC_K8S_CA="$$(base64 < $(TESTBUILD_DIR)/mock-ca.crt | tr -d '\n')" \
	  TF_ACC_K8S_CMD="$(OIDC_DIR)/get-token.sh" \
	  TF_ACC_KUBECONFIG="$$(cat $(TESTBUILD_DIR)/kubeconfig.yaml)" \
	  TF_ACC_K8S_TOKEN="$$(cat $(TESTBUILD_DIR)/sa-token.txt)" \
	  TF_ACC_K8S_CLIENT_CERT="$$(base64 < $(TESTBUILD_DIR)/client.crt | tr -d '\n')" \
	  TF_ACC_K8S_CLIENT_KEY="$$(base64 < $(TESTBUILD_DIR)/client.key | tr -d '\n')"; \
	echo "TF_ACC_TERRAFORM_PATH=$$TF_ACC_TERRAFORM_PATH"; \
	echo "TF_ACC_K8S_HOST=$$TF_ACC_K8S_HOST"; \
	echo "TF_ACC_K8S_CA=$$(echo $$TF_ACC_K8S_CA | cut -c1-20)..."; \
	echo "TF_ACC_K8S_CMD=$$TF_ACC_K8S_CMD"; \
	echo "TF_ACC_KUBECONFIG=$$(echo $$TF_ACC_KUBECONFIG | cut -c1-20)..."; \
	echo "TF_ACC_K8S_TOKEN=$$(echo $$TF_ACC_K8S_TOKEN | cut -c1-20)..."; \
	echo "TF_ACC_K8S_CLIENT_CERT=$$(echo $$TF_ACC_K8S_CLIENT_CERT | cut -c1-20)..."; \
	echo "TF_ACC_K8S_CLIENT_KEY=$$(echo $$TF_ACC_K8S_CLIENT_KEY | cut -c1-20)..."; \
	go test -cover -v ./internal/k8sconnect/... -timeout 30m -run "$$TEST_FILTER" $$PARALLEL_FLAG

.PHONY: test-examples
test-examples: create-cluster install
	@echo "📚 Testing examples directory..."; \
	TEST_FILTER="$${TEST:-}"; \
	if [ -n "$$TEST_FILTER" ]; then \
		echo "Running tests matching: $$TEST_FILTER"; \
	else \
		echo "Running all example tests"; \
	fi; \
	cd test/examples && \
	TF_ACC_KUBECONFIG="$$(cat ../../.testbuild/kubeconfig.yaml)" \
	go test -v -timeout 30m -run "$$TEST_FILTER"

.PHONY: test-docs-examples
test-docs-examples: create-cluster install
	@echo "📖 Testing documentation examples..."; \
	cd test/doctest && \
	TF_ACC_KUBECONFIG="$$(cat ../../.testbuild/kubeconfig.yaml)" \
	go test -v -timeout 30m

.PHONY: test-diagnostics
test-diagnostics: create-cluster install
	@echo "🔬 Testing diagnostic output (warnings/errors)..."; \
	TEST_FILTER="$${TEST:-}"; \
	if [ -n "$$TEST_FILTER" ]; then \
		echo "Running tests matching: $$TEST_FILTER"; \
	else \
		echo "Running all diagnostic tests"; \
	fi; \
	cd test/diagnostics && \
	TF_ACC_KUBECONFIG="$$(cat ../../.testbuild/kubeconfig.yaml)" \
	go test -v -timeout 30m -run "$$TEST_FILTER"

.PHONY: clean
clean:
	-docker rm -f dex
	-k3d cluster delete k8sconnect-test
	-rm -rf $(TESTBUILD_DIR) $(DEX_SSL_DIR)
	-rm -rf bin/

.PHONY: vet
vet:
	@echo "🔍 Running go vet on all packages"
	@go vet ./...

.PHONY: docs
docs:
	@echo "📚 Generating provider documentation..."
	@if ! command -v tfplugindocs >/dev/null 2>&1; then \
		echo "Installing tfplugindocs..."; \
		go install github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@latest; \
	fi
	PATH="$$(go env GOPATH)/bin:$$PATH" tfplugindocs

.PHONY: lint
lint: vet
	@echo "🔍 Running golangci-lint..."
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "Installing golangci-lint..."; \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | \
			sh -s -- -b $$(go env GOPATH)/bin; \
	fi
	PATH="$$(go env GOPATH)/bin:$$PATH" golangci-lint run --timeout=5m --fix

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
	PATH="$$(go env GOPATH)/bin:$$PATH" gosec -quiet ./...
	PATH="$$(go env GOPATH)/bin:$$PATH" govulncheck ./...

.PHONY: deadcode
deadcode: ## Find unused/dead code
	@echo "🔍 Checking for unused code. Beware of false positives..."
	@go run golang.org/x/tools/cmd/deadcode@latest ./...

.PHONY: release-dry-run
release-dry-run:
	@echo "🔍 Testing release process locally..."
	@if ! command -v goreleaser >/dev/null 2>&1; then \
		echo "Installing goreleaser..."; \
		go install github.com/goreleaser/goreleaser/v2@latest; \
	fi
	PATH="$$(go env GOPATH)/bin:$$PATH" goreleaser release --snapshot --skip=publish --clean

.PHONY: release-check
release-check:
	@echo "✅ Checking release configuration..."
	@if ! command -v goreleaser >/dev/null 2>&1; then \
		echo "Installing goreleaser..."; \
		go install github.com/goreleaser/goreleaser/v2@latest; \
	fi
	PATH="$$(go env GOPATH)/bin:$$PATH" goreleaser check

.PHONY: coverage
coverage: create-cluster
	@echo "📊 Building coverage reports (unit + acceptance)"
	@TF_VERSION="$${TF_ACC_TERRAFORM_VERSION:-$(TERRAFORM_VERSION)}"; \
	echo "Required Terraform version: $$TF_VERSION"; \
	if command -v tfenv >/dev/null 2>&1; then \
		echo "📦 tfenv detected - switching to Terraform $$TF_VERSION"; \
		if ! tfenv list 2>/dev/null | grep -q "$$TF_VERSION"; then \
			echo "   Installing Terraform $$TF_VERSION..."; \
			tfenv install $$TF_VERSION; \
		fi; \
		tfenv use $$TF_VERSION; \
	else \
		echo "ℹ️  tfenv not found - using system Terraform"; \
	fi; \
	ACTUAL_TF_VERSION=$$(terraform version -json 2>/dev/null | jq -r .terraform_version || terraform version | head -n1 | cut -d' ' -f2 | tr -d 'v'); \
	echo "Actual Terraform version: $$ACTUAL_TF_VERSION"; \
	if [ "$$ACTUAL_TF_VERSION" != "$$TF_VERSION" ]; then \
		echo "❌ ERROR: Terraform version mismatch!"; \
		echo "   Expected: $$TF_VERSION"; \
		echo "   Actual:   $$ACTUAL_TF_VERSION"; \
		echo "   Ensure Terraform $$TF_VERSION is installed"; \
		exit 1; \
	fi; \
	echo "✅ Terraform version verified"; \
	PKGS=$$(go list ./... | grep -v -E "(test/examples|test/doctest)"); \
	echo "🧪 Running unit tests..."; \
	go test $$PKGS -run "^Test[^A]" -coverpkg=./... -covermode=atomic -coverprofile=coverage-unit.out -count=1 || true; \
	echo "🎯 Running acceptance tests..."; \
	TF_ACC=1 \
	  TF_ACC_TERRAFORM_PATH="$$(which terraform)" \
	  TF_ACC_K8S_HOST="$$(cat $(TESTBUILD_DIR)/cluster-endpoint.txt)" \
	  TF_ACC_K8S_CA="$$(base64 < $(TESTBUILD_DIR)/mock-ca.crt | tr -d '\n')" \
	  TF_ACC_K8S_CMD="$(OIDC_DIR)/get-token.sh" \
	  TF_ACC_KUBECONFIG="$$(cat $(TESTBUILD_DIR)/kubeconfig.yaml)" \
	  TF_ACC_K8S_TOKEN="$$(cat $(TESTBUILD_DIR)/sa-token.txt)" \
	  TF_ACC_K8S_CLIENT_CERT="$$(base64 < $(TESTBUILD_DIR)/client.crt | tr -d '\n')" \
	  TF_ACC_K8S_CLIENT_KEY="$$(base64 < $(TESTBUILD_DIR)/client.key | tr -d '\n')" \
	  go test $$PKGS -run "^TestAcc" -coverpkg=./... -covermode=atomic -coverprofile=coverage-acceptance.out -count=1 ; \
	echo "📊 Merging coverage reports..."; \
	if ! command -v gocovmerge >/dev/null 2>&1; then \
		echo "Installing gocovmerge..."; \
		go install github.com/wadey/gocovmerge@latest; \
	fi; \
	PATH="$$(go env GOPATH)/bin:$$PATH" gocovmerge coverage-unit.out coverage-acceptance.out > coverage.out; \
	go tool cover -func=coverage.out ; \
	go tool cover -html=coverage.out -o coverage.html ; \
	echo "HTML report written to ./coverage.html"; \
	echo "Separate reports: coverage-unit.out, coverage-acceptance.out"

.PHONY: complexity
complexity: ## Check code complexity
	@go run github.com/fzipp/gocyclo/cmd/gocyclo@latest -over 15 . | sort -rn || true

.PHONY: loc
loc: ## Count lines of code by file type
	#@go run github.com/boyter/scc/v3@latest . --exclude-dir=vendor,node_modules,.git --not-match=".*[Tt]est.*" --sort lines
	@go run github.com/boyter/scc/v3@latest . --exclude-dir=vendor,node_modules,.git --sort lines

