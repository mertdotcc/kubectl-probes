#!/usr/bin/env bash
# Apply the workloads, retrying past ingress-nginx's admission webhook.
#
# The controller's readiness probe checks :10254 while its validating webhook
# listens on :8443, and that listener comes up later. So the pod reports Ready,
# the admission Service gets a ready endpoint, and an Ingress apply still gets
# "connection refused" because nothing is accepting on 8443 yet.
#
# Waiting harder does not fix it: no condition, endpoint or event marks the
# moment :8443 starts accepting. Retrying does.
#
# A readiness probe that answers for something other than what callers need is,
# of course, the exact thing the plugin in this repo was written to make
# visible. It seemed rude not to mention it.
set -euo pipefail

CLUSTER=probes-demo
KUBECTL=(kubectl --context "kind-${CLUSTER}")
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ATTEMPTS=40

# The CA bundle the webhook is validated against is written by this Job. It may
# already have been cleaned up, which is fine.
echo "==> waiting for the admission certificate job"
"${KUBECTL[@]}" -n ingress-nginx wait --for=condition=Complete \
  job/ingress-nginx-admission-patch --timeout=120s 2>/dev/null \
  || echo "    job already gone, carrying on"

echo "==> applying workloads"
for attempt in $(seq 1 "${ATTEMPTS}"); do
  if out=$("${KUBECTL[@]}" apply -k "${HERE}/manifests" 2>&1); then
    echo "${out}"
    [ "${attempt}" -gt 1 ] && echo "==> applied on attempt ${attempt}"
    exit 0
  fi

  # Anything that is not the admission webhook is a real failure. Say so now
  # rather than burning three minutes on it.
  if ! grep -q "validate.nginx.ingress.kubernetes.io" <<<"${out}"; then
    echo "${out}" >&2
    echo "==> failed for a reason that is not the admission webhook" >&2
    exit 1
  fi

  printf "\r    admission webhook still refusing connections (%d/%d)" \
    "${attempt}" "${ATTEMPTS}"
  sleep 5
done

echo
echo "==> gave up after ${ATTEMPTS} attempts; is the ingress controller healthy?" >&2
"${KUBECTL[@]}" -n ingress-nginx get pods >&2
exit 1
