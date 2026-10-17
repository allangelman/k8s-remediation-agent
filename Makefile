.DEFAULT_GOAL := help

# Override per command (`make up CLUSTER=foo`) or for the shell (`export CLUSTER=foo`).
CLUSTER        ?= remediation
CONTEXT        := kind-$(CLUSTER)
ARGOCD_VERSION := v3.5.5
# Branch ArgoCD deploys from; see infra/argocd-app.yaml.
ENV_BRANCH     := demo-env

# Always target the kind cluster explicitly, never whatever context is current.
KUBECTL := kubectl --context $(CONTEXT)

.PHONY: help build test agent up down reset status argocd-ui break-image cluster argocd env-branch app refresh

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'

build: ## Build the agent binary into bin/
	go build -o bin/agent ./cmd/agent

test: ## Run Go tests
	go test ./...

# Extra flags for the agent binary, e.g. make agent ARGS="--model claude-sonnet-5-5"
ARGS ?=

agent: build ## Run the agent: diagnose demo-app, fix it, and verify (needs ANTHROPIC_API_KEY)
	./bin/agent --context $(CONTEXT) $(ARGS)

up: cluster argocd env-branch app refresh ## Create the cluster, install ArgoCD, and deploy the demo app
	@CONTEXT=$(CONTEXT) scripts/wait-for-app.sh Healthy

down: ## Delete the kind cluster
	kind delete cluster --name $(CLUSTER)

reset: env-branch refresh ## Undo any fault: reset demo-env to main and wait for Healthy
	@CONTEXT=$(CONTEXT) scripts/wait-for-app.sh Healthy

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

# Faults start with a reset so every run begins from main's history. Otherwise
# earlier faults and the agent's reverts stay in demo-env's history, and the
# agent (reasonably) treats a re-pushed change differently.
break-image: reset ## Fault 1: push a nonexistent image tag (ImagePullBackOff)
	@ENV_BRANCH=$(ENV_BRANCH) faults/bad-image-tag.sh
	@$(MAKE) --no-print-directory refresh
	@CONTEXT=$(CONTEXT) scripts/wait-for-app.sh Degraded

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

# Force-push main to demo-env, discarding any fault commits on it.
env-branch:
	git fetch -q origin main
	git push -q --force origin origin/main:refs/heads/$(ENV_BRANCH)

app:
	$(KUBECTL) apply -f infra/argocd-app.yaml

# ArgoCD only notices new commits when it polls the Git repo, every 3 minutes
# by default, so a fault or reset could sit for up to 3 minutes before anything
# happens. The refresh annotation tells ArgoCD to re-read demo-env right away and
# compare it with the cluster; with auto-sync on, it then deploys any new commit
# immediately. ArgoCD removes the annotation once the refresh is done.
refresh:
	@$(KUBECTL) -n argocd annotate application demo-app argocd.argoproj.io/refresh=normal --overwrite >/dev/null
