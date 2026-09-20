# The kubectl-probes demo cluster. See README.md, and ADR 0004 for why it
# looks like this.

CLUSTER      := probes-demo
KUBECTL      := kubectl --context kind-$(CLUSTER)
INGRESS_REF  := controller-v1.11.2
INGRESS_URL  := https://raw.githubusercontent.com/kubernetes/ingress-nginx/$(INGRESS_REF)/deploy/static/provider/kind/deploy.yaml
REPO_ROOT    := $(shell git rev-parse --show-toplevel)

.DEFAULT_GOAL := help
.PHONY: help up down stop start chaos heal probes build status urls

help: ## Show this help
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | awk -F':.*?## ' '{printf "  \033[36m%-8s\033[0m %s\n", $$1, $$2}'

up: ## Create the cluster and everything in it
	kind create cluster --config kind.yaml
	@echo "==> ingress-nginx ($(INGRESS_REF))"
	$(KUBECTL) apply -f $(INGRESS_URL)
	@echo "==> waiting for the ingress admission webhook, or the Ingresses will not apply"
	$(KUBECTL) wait --namespace ingress-nginx \
		--for=condition=Ready pod \
		--selector=app.kubernetes.io/component=controller \
		--timeout=180s
	@echo "==> workloads"
	$(KUBECTL) apply -k manifests/
	@echo "==> waiting for the shop to come up"
	-$(KUBECTL) wait --namespace shop --for=condition=Available deployment --all --timeout=300s
	-$(KUBECTL) rollout status --namespace shop statefulset/postgres --timeout=300s
	@$(MAKE) --no-print-directory urls

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

urls: ## Where to point a browser
	@echo
	@echo "  shop        http://shop.localtest.me:8080"
	@echo "  api         http://api.localtest.me:8080"
	@echo "  grafana     http://grafana.localtest.me:8080"
	@echo "  prometheus  http://prometheus.localtest.me:8080"
	@echo
	@echo "  If localtest.me does not resolve for you, see README.md."
	@echo
