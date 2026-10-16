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

argocd-ui: ## Port-forward the ArgoCD UI to https://localhost:8080 and print the admin password
	@echo "User: admin"
	@echo "Password: $$($(KUBECTL) -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d)"
	@echo "Open https://localhost:8080 (accept the self-signed certificate). Ctrl-C to stop."
	$(KUBECTL) -n argocd port-forward svc/argocd-server 8080:443

cluster:
	@if kind get clusters | grep -qx $(CLUSTER); then \
		echo "kind cluster '$(CLUSTER)' already exists"; \
	else \
		kind create cluster --name $(CLUSTER) --config infra/kind-config.yaml; \
	fi

argocd:
	$(KUBECTL) create namespace argocd --dry-run=client -o yaml | $(KUBECTL) apply -f -
	# --server-side: ArgoCD's CRDs are too large for client-side apply.
	$(KUBECTL) apply -n argocd --server-side --force-conflicts \
		-f https://raw.githubusercontent.com/argoproj/argo-cd/$(ARGOCD_VERSION)/manifests/install.yaml
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
