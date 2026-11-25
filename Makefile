# Entry point for the local environment. Requires: multipass, kubectl.
# Usage: make up  →  export KUBECONFIG=$(PWD)/kubeconfig  →  make status

VM              ?= rke2
CPUS            ?= 4
MEM             ?= 8G
DISK            ?= 40G
KUBECONFIG_FILE := $(CURDIR)/kubeconfig

# Primary IPv4 of the VM (first one listed; CNI interfaces appear after it).
VM_IP = $$(multipass info $(VM) | awk '/IPv4/ {print $$2; exit}')

.DEFAULT_GOAL := help
.PHONY: help up kubeconfig status down

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
