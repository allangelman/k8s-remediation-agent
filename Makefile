.DEFAULT_GOAL := help

# Override per command (`make up CLUSTER=foo`) or for the shell (`export CLUSTER=foo`).
CLUSTER        ?= remediation
CONTEXT        := kind-$(CLUSTER)
ARGOCD_VERSION := v3.5.5
# Branch ArgoCD syncs from. Defaults to the current branch so PR branches can be
# tested before merging; the branch must be pushed to GitHub.
REVISION       ?= $(shell git rev-parse --abbrev-ref HEAD)

# Always target the kind cluster explicitly, never whatever context is current.
KUBECTL := kubectl --context $(CONTEXT)

.PHONY: help build test up down status argocd-ui cluster argocd app wait

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'

build: ## Build the agent binary into bin/
	go build -o bin/agent ./cmd/agent

test: ## Run Go tests
	go test ./...

up: cluster argocd app wait ## Create the cluster, install ArgoCD, and deploy the demo app
	@echo "Ready: demo-app is Synced and Healthy (tracking branch '$(REVISION)')."

down: ## Delete the kind cluster
	kind delete cluster --name $(CLUSTER)

status: ## Show the ArgoCD app state and demo pods
	@$(KUBECTL) -n argocd get application demo-app \
		-o custom-columns=APP:.metadata.name,REVISION:.spec.source.targetRevision,SYNC:.status.sync.status,HEALTH:.status.health.status
	@echo
	@$(KUBECTL) -n demo get pods

argocd-ui: ## Port-forward the ArgoCD UI to http://localhost:8080 and print the admin password
	@echo "User: admin"
	@echo "Password: $$($(KUBECTL) -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d)"
	@echo "Open http://localhost:8080. Ctrl-C to stop."
	$(KUBECTL) -n argocd port-forward svc/argocd-server 8080:80

cluster:
	@if kind get clusters | grep -qx $(CLUSTER); then \
		echo "kind cluster '$(CLUSTER)' already exists"; \
	else \
		kind create cluster --name $(CLUSTER) --config infra/kind-config.yaml; \
	fi

# Installs ArgoCD and configures it for local use.
#
# --server-side: ArgoCD's CRDs are too large for client-side apply.
#
# server.insecure: serve the ArgoCD UI over plain HTTP. By default ArgoCD uses a
# self-signed certificate, which browsers reject with a "connection is not
# private" warning. That's safe to drop here: the UI is only reachable through a
# local `kubectl port-forward` to a throwaway kind cluster. A real deployment
# would keep TLS with a trusted certificate (e.g. cert-manager + Let's Encrypt).
# argocd-server only reads this setting at startup, so restart it on change.
argocd:
	$(KUBECTL) create namespace argocd --dry-run=client -o yaml | $(KUBECTL) apply -f -
	$(KUBECTL) apply -n argocd --server-side --force-conflicts \
		-f https://raw.githubusercontent.com/argoproj/argo-cd/$(ARGOCD_VERSION)/manifests/install.yaml
	@if $(KUBECTL) -n argocd patch configmap argocd-cmd-params-cm --type merge \
		-p '{"data":{"server.insecure":"true"}}' | grep -q 'no change'; then \
		echo "argocd-server already serving plain HTTP"; \
	else \
		$(KUBECTL) -n argocd rollout restart deploy/argocd-server; \
	fi
	$(KUBECTL) -n argocd rollout status deploy/argocd-server --timeout=300s
	$(KUBECTL) -n argocd rollout status deploy/argocd-repo-server --timeout=300s
	$(KUBECTL) -n argocd rollout status statefulset/argocd-application-controller --timeout=300s

app:
	@git ls-remote --exit-code --heads origin $(REVISION) >/dev/null || \
		{ echo "Branch '$(REVISION)' is not on GitHub; push it first (ArgoCD syncs from GitHub)."; exit 1; }
	sed 's|__REVISION__|$(REVISION)|' infra/argocd-app.yaml | $(KUBECTL) apply -f -

wait:
	@echo "Waiting for demo-app to be Synced and Healthy..."
	@for i in $$(seq 1 60); do \
		sync=$$($(KUBECTL) -n argocd get application demo-app -o jsonpath='{.status.sync.status}'); \
		health=$$($(KUBECTL) -n argocd get application demo-app -o jsonpath='{.status.health.status}'); \
		if [ "$$sync" = "Synced" ] && [ "$$health" = "Healthy" ]; then exit 0; fi; \
		echo "  sync=$${sync:-unknown} health=$${health:-unknown}"; \
		sleep 5; \
	done; \
	echo "Timed out waiting for demo-app. Run 'make status' to investigate."; exit 1
