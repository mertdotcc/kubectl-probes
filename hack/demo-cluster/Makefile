# The kubectl-probes demo cluster. See README.md, and ADR 0004 for why it
# looks like this.

CLUSTER      := probes-demo
KUBECTL      := kubectl --context kind-$(CLUSTER)
REPO_ROOT    := $(shell git rev-parse --show-toplevel)

.DEFAULT_GOAL := help
.PHONY: help up cluster apply ready down stop start chaos heal probes build status

help: ## Show this help
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | awk -F':.*?## ' '{printf "  \033[36m%-8s\033[0m %s\n", $$1, $$2}'

up: cluster apply ready ## Create the cluster and everything in it

cluster: ## Just the kind cluster
	kind create cluster --config kind.yaml

apply: ## Just the workloads. Safe to re-run
	$(KUBECTL) apply -k manifests

ready: ## Wait for the shop to come up
	@echo "==> waiting for the shop"
	-$(KUBECTL) wait --namespace shop --for=condition=Available deployment --all --timeout=300s
	-$(KUBECTL) rollout status --namespace shop statefulset/postgres --timeout=300s

down: ## Delete the cluster entirely
	kind delete cluster --name $(CLUSTER)

stop: ## Suspend the cluster, keeping its state
	docker stop $$(kind get nodes --name $(CLUSTER) | tr '\n' ' ')

start: ## Resume a suspended cluster
	docker start $$(kind get nodes --name $(CLUSTER) | tr '\n' ' ')
	@echo "==> give the control plane a moment, then: make probes"

chaos: ## Break some probes on purpose, so there is failure evidence to look at
	./scripts/chaos.sh

heal: ## Undo chaos
	./scripts/chaos.sh --heal

build: ## Build the plugin from this repo
	cd $(REPO_ROOT) && go build -o kubectl-probes .

probes: build ## Run the plugin against the demo cluster
	cd $(REPO_ROOT) && ./kubectl-probes --context kind-$(CLUSTER) -A

status: ## What is actually running
	$(KUBECTL) get pods -A -o wide
