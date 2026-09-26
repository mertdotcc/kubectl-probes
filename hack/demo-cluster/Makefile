# The kubectl-probes demo cluster. See README.md, and ADR 0004 for why it
# looks like this.
#
# kind by default. Every target also runs on minikube:
#
#   make up TOOL=minikube
#
# or export TOOL=minikube once for the shell.

CLUSTER      := probes-demo
TOOL         ?= kind
REPO_ROOT    := $(shell git rev-parse --show-toplevel)

ifeq ($(TOOL),kind)
CONTEXT      = kind-$(CLUSTER)
CREATE       = kind create cluster --config kind.yaml --name $(CLUSTER)
DELETE       = kind delete cluster --name $(CLUSTER)
SUSPEND      = docker stop $$(kind get nodes --name $(CLUSTER) | tr '\n' ' ')
RESUME       = docker start $$(kind get nodes --name $(CLUSTER) | tr '\n' ' ')
else ifeq ($(TOOL),minikube)
# minikube has no config file, so what kind.yaml pins is spelled out here: three
# nodes, the same Kubernetes, and events kept for a day rather than an hour.
CONTEXT      = $(CLUSTER)
CREATE       = minikube start -p $(CLUSTER) --driver=docker --nodes=3 --cpus=2 --memory=3g \
                 --kubernetes-version=v1.37.0 --extra-config=apiserver.event-ttl=24h
DELETE       = minikube delete -p $(CLUSTER)
SUSPEND      = minikube stop -p $(CLUSTER)
RESUME       = minikube start -p $(CLUSTER)
else
$(error TOOL is "$(TOOL)"; it must be kind or minikube)
endif

KUBECTL      = kubectl --context $(CONTEXT)

.DEFAULT_GOAL := help
.PHONY: help up cluster apply ready down stop start chaos heal probes build status

help: ## Show this help
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | awk -F':.*?## ' '{printf "  \033[36m%-8s\033[0m %s\n", $$1, $$2}'
	@echo
	@echo "  TOOL=kind (the default) or TOOL=minikube picks what runs the cluster."

up: cluster apply ready ## Create the cluster and everything in it

cluster: ## Just the cluster
	$(CREATE)

apply: ## Just the workloads. Safe to re-run
	$(KUBECTL) apply -k manifests

ready: ## Wait for the shop to come up
	@echo "==> waiting for the shop"
	-$(KUBECTL) wait --namespace shop --for=condition=Available deployment --all --timeout=300s
	-$(KUBECTL) rollout status --namespace shop statefulset/postgres --timeout=300s

down: ## Delete the cluster entirely
	$(DELETE)

stop: ## Suspend the cluster, keeping its state
	$(SUSPEND)

start: ## Resume a suspended cluster
	$(RESUME)
	@echo "==> give the control plane a moment, then: make probes"

chaos: ## Break some probes on purpose, so there is failure evidence to look at
	CONTEXT=$(CONTEXT) TOOL=$(TOOL) ./scripts/chaos.sh

heal: ## Undo chaos
	CONTEXT=$(CONTEXT) TOOL=$(TOOL) ./scripts/chaos.sh --heal

build: ## Build the plugin from this repo
	cd $(REPO_ROOT) && go build -o kubectl-probes .

probes: build ## Run the plugin against the demo cluster
	cd $(REPO_ROOT) && ./kubectl-probes --context $(CONTEXT) -A

status: ## What is actually running
	$(KUBECTL) get pods -A -o wide
