#!/usr/bin/env bash
# Break probes on purpose, so there is fresh failure evidence to look at.
#
# Events are garbage-collected on the API server's --event-ttl, so failure
# evidence cannot be preserved across a suspend. It is generated on demand
# instead, seconds before you capture output. See ADR 0004.
#
# Two different failures, because they read differently in the plugin:
#
#   catalog  liveness points at a path that 404s. The kubelet restarts the
#            container on a loop: restart counts climb, Unhealthy events name
#            the liveness probe, and the last termination reason is filled in.
#
#   web      readiness points at a path that 404s. Nothing is restarted. The
#            pods simply leave the Service and stay running, which is the
#            failure people find hardest to spot.
#
# Nothing here reaches inside a container, so it does not depend on what
# tooling the image happens to ship.
set -euo pipefail

CLUSTER=probes-demo
KUBECTL=(kubectl --context "kind-${CLUSTER}")

patch_path() { # deployment namespace probe value
  "${KUBECTL[@]}" -n "$2" patch deployment "$1" --type=json \
    -p "[{\"op\":\"replace\",\"path\":\"/spec/template/spec/containers/0/$3/httpGet/path\",\"value\":\"$4\"}]" >/dev/null
}

if [[ "${1:-}" == "--heal" ]]; then
  echo "==> restoring catalog liveness and web readiness"
  patch_path catalog shop livenessProbe  /healthz
  patch_path web     shop readinessProbe /readyz
  "${KUBECTL[@]}" -n shop rollout status deployment/catalog --timeout=120s
  "${KUBECTL[@]}" -n shop rollout status deployment/web --timeout=120s
  echo "==> healed. Restart counts stay where they are; that is the point of them."
  exit 0
fi

echo "==> pointing catalog's liveness probe at a path that does not exist"
patch_path catalog shop livenessProbe /healthz-broken

echo "==> pointing web's readiness probe at a path that does not exist"
patch_path web shop readinessProbe /readyz-broken

echo "==> waiting for the kubelet to notice (this takes about a minute)"
# Only the broken paths answer 404. Counting every Unhealthy event would count
# the connection-refused ones a freshly started shop records on its way up, and
# stop waiting before either broken probe has run.
deadline=$((SECONDS + 180))
while (( SECONDS < deadline )); do
  events=$("${KUBECTL[@]}" -n shop get events --field-selector reason=Unhealthy \
    -o jsonpath='{range .items[*]}{.message}{"\n"}{end}' 2>/dev/null | grep -c 'statuscode: 404' || true)
  if (( events >= 2 )); then
    echo "==> ${events} Unhealthy events recorded"
    break
  fi
  sleep 5
done

if (( events < 2 )); then
  echo "==> gave up waiting; the probes are broken, evidence may still be landing" >&2
fi

cat <<'MSG'

Now look at it:

  kubectl probes -n shop
  kubectl probes -n shop deploy/catalog     # restarts, and liveness named in the evidence
  kubectl probes -n shop deploy/web         # never restarted, quietly out of service

Put it back with:

  make heal
MSG
