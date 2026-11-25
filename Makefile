# Entry point for the local environment. Requires: multipass, kubectl.
# Usage: make up  →  export KUBECONFIG=$(PWD)/kubeconfig  →  make status

VM              ?= rke2
CPUS            ?= 4
MEM             ?= 8G
DISK            ?= 40G
KUBECONFIG_FILE := $(CURDIR)/kubeconfig

# Primary IPv4 of the VM (first one listed; CNI interfaces appear after it).
VM_IP = $$(multipass info $(VM) | awk '/IPv4/ {print $$2; exit}')

# Target for `make load`. Defaults to the app's Ingress on the VM (step 4+);
# override for a local run: make load URL=http://localhost:8080
URL ?= http://$(VM_IP)

# App image. The tag is the current commit, so every image traces back to its source.
IMAGE     ?= ghcr.io/devjuank/workload-api
TAG       ?= sha-$(shell git rev-parse --short HEAD)
PLATFORMS ?= linux/arm64,linux/amd64
BUILDER   ?= multiarch
REPO_URL  := https://github.com/devjuank/rke2-gitops-observability

.DEFAULT_GOAL := help
.PHONY: help up kubeconfig status down deploy dns-check test run load image

help: ## Show available targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F ':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

up: ## Create the VM, install RKE2 via cloud-init, fetch kubeconfig
	@command -v multipass >/dev/null || { echo "multipass not found: brew install --cask multipass"; exit 1; }
	@if multipass info $(VM) >/dev/null 2>&1; then \
		echo "VM '$(VM)' already exists (run 'make down' to recreate)"; \
	else \
		multipass launch 24.04 --name $(VM) --cpus $(CPUS) --memory $(MEM) --disk $(DISK) \
			--timeout 900 --cloud-init infra/cloud-init.yaml; \
	fi
	@$(MAKE) --no-print-directory kubeconfig

kubeconfig: ## Copy the admin kubeconfig to ./kubeconfig, pointing at the VM IP
	@multipass exec $(VM) -- sudo cat /etc/rancher/rke2/rke2.yaml \
		| sed "s/127.0.0.1/$(VM_IP)/" > $(KUBECONFIG_FILE)
	@chmod 600 $(KUBECONFIG_FILE)
	@echo "Wrote $(KUBECONFIG_FILE). Run: export KUBECONFIG=$(KUBECONFIG_FILE)"

status: ## Show node and pod status
	@KUBECONFIG=$(KUBECONFIG_FILE) kubectl get nodes -o wide
	@KUBECONFIG=$(KUBECONFIG_FILE) kubectl get pods -A

down: ## Delete the VM and the local kubeconfig
	multipass delete --purge $(VM)
	rm -f $(KUBECONFIG_FILE)

deploy: ## Apply the app manifests and wait for the rollout
	KUBECONFIG=$(KUBECONFIG_FILE) kubectl apply -k deploy/app
	KUBECONFIG=$(KUBECONFIG_FILE) kubectl -n app rollout status deploy/workload-api --timeout=180s
	@$(MAKE) --no-print-directory dns-check
	@echo "App: http://app.$(VM_IP).nip.io/"

# Some routers drop public DNS answers that point to private IPs ("DNS rebinding
# protection"), which breaks nip.io. Detect it and print the one-time fix.
dns-check: ## Check that <name>.<VM-IP>.nip.io resolves on this machine
	@curl -s -o /dev/null --connect-timeout 5 "http://app.$(VM_IP).nip.io/"; \
	if [ $$? -eq 6 ]; then \
		echo ""; \
		echo "WARNING: nip.io names don't resolve here (likely your router's DNS rebinding protection)."; \
		echo "One-time fix on macOS (only *.nip.io lookups go to 1.1.1.1; everything else is unchanged):"; \
		echo "  sudo mkdir -p /etc/resolver && echo 'nameserver 1.1.1.1' | sudo tee /etc/resolver/nip.io"; \
		echo "Until then, the app also answers on http://$(VM_IP)/"; \
		echo ""; \
	else \
		echo "nip.io resolves OK"; \
	fi

test: ## Vet and test the Go app (with the race detector)
	cd app && go vet ./... && go test -race ./...

run: ## Run the app locally on :8080 (env vars like STARTUP_DELAY_SECONDS pass through)
	cd app && go run .

load: ## Send traffic to / and /work until Ctrl-C (URL=... to override)
	@echo "Sending load to $(URL) — Ctrl-C to stop"
	@while true; do \
		for i in 1 2 3 4 5; do curl -s -o /dev/null -w '%{http_code} ' "$(URL)/work" & done; \
		curl -s -o /dev/null "$(URL)/"; \
		wait; \
	done

image: ## Build and push the multi-arch app image (needs: docker login ghcr.io)
	@git diff --quiet HEAD -- app/ || { echo "app/ has uncommitted changes: commit first so $(TAG) matches the source"; exit 1; }
	@# The default "docker" driver can't push multi-platform images; docker-container can.
	@docker buildx inspect $(BUILDER) >/dev/null 2>&1 || docker buildx create --name $(BUILDER) --driver docker-container >/dev/null
	docker buildx build --builder $(BUILDER) --platform $(PLATFORMS) \
		--build-arg VERSION=$(TAG) \
		--label org.opencontainers.image.source=$(REPO_URL) \
		-t $(IMAGE):$(TAG) --push app/
