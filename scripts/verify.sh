#!/usr/bin/env bash
# Automated acceptance checks (docs/SPEC.md §6). Run it with `make verify`,
# which sets KUBECONFIG and VM_IP. Exits non-zero if any check fails.
set -uo pipefail

: "${VM_IP:?VM_IP is not set (run via make verify)}"
failed=0

pass() { printf '  \033[32m✔\033[0m %s\n' "$1"; }
fail() { printf '  \033[31m✘\033[0m %s\n' "$1"; failed=1; }
warn() { printf '  \033[33m!\033[0m %s\n' "$1"; }

# check <description> <function> [tries]: retries every 5s (for things that
# take a moment right after a deploy, like Prometheus' first scrape).
check() {
  local desc=$1 fn=$2 tries=${3:-1} i
  for ((i = 1; i <= tries; i++)); do
    if "$fn" >/dev/null 2>&1; then pass "$desc"; return; fi
    ((i < tries)) && sleep 5
  done
  fail "$desc"
}

# GET http://<name>.<VM_IP>.nip.io<path> and print the status code. --resolve
# skips DNS, so a nip.io/router problem doesn't hide the cluster's real state.
http_code() {
  local name=$1 path=$2; shift 2
  curl -s -o /dev/null -w '%{http_code}' --max-time 5 \
    --resolve "$name.$VM_IP.nip.io:80:$VM_IP" "$@" "http://$name.$VM_IP.nip.io$path"
}

# Instant PromQL query; prints the first sample's value.
promql() {
  curl -s --max-time 5 --resolve "prometheus.$VM_IP.nip.io:80:$VM_IP" \
    --data-urlencode "query=$1" "http://prometheus.$VM_IP.nip.io/api/v1/query" |
    sed -n 's/.*"value":\[[^,]*,"\([^"]*\)"\].*/\1/p'
}

deploy_field() { kubectl -n app get deploy workload-api -o jsonpath="{.spec.template.spec.containers[0].$1}"; }

# --- Checks -------------------------------------------------------------------

node_ready()      { [ "$(kubectl get nodes -o jsonpath='{.items[*].status.conditions[?(@.type=="Ready")].status}')" = True ]; }
pods_healthy()    { [ -z "$(kubectl get pods -A --no-headers --field-selector=status.phase!=Running,status.phase!=Succeeded)" ]; }
app_ready()       { [ "$(kubectl -n app get deploy workload-api -o jsonpath='{.status.readyReplicas}/{.spec.replicas}')" = 2/2 ]; }
app_probes()      { [ "$(deploy_field readinessProbe.httpGet.path) $(deploy_field livenessProbe.httpGet.path)" = "/readyz /healthz" ]; }
app_resources()   { [ -n "$(deploy_field resources.requests.cpu)" ] && [ -n "$(deploy_field resources.limits.memory)" ]; }
app_strategy()    { [ "$(kubectl -n app get deploy workload-api -o jsonpath='{.spec.strategy.rollingUpdate.maxSurge}/{.spec.strategy.rollingUpdate.maxUnavailable}')" = 1/0 ]; }
app_http()        { [ "$(http_code app /)" = 200 ] && [ "$(http_code app /healthz)" = 200 ] && [ "$(http_code app /readyz)" = 200 ]; }
grafana_login()   {
  local pw; pw=$(kubectl -n monitoring get secret grafana-admin -o jsonpath='{.data.admin-password}' | base64 -d)
  [ "$(http_code grafana /api/user -u "admin:$pw")" = 200 ]
}
target_up()       { [ "$(promql 'count(up{job="workload-api"} == 1)')" = 2 ]; }
requests_metric() { [ -n "$(promql 'count(http_requests_total{job="workload-api"})')" ]; }
latency_metric()  { [ -n "$(promql 'count(http_request_duration_seconds_bucket{job="workload-api"})')" ]; }

echo "Cluster (AC-1)"
check "node Ready" node_ready
check "all pods Running or Completed" pods_healthy 6

echo "App (AC-2..AC-5)"
check "workload-api: 2/2 pods Ready" app_ready
check "readiness (/readyz) and liveness (/healthz) probes configured" app_probes
check "CPU/memory requests and limits set" app_resources
check "RollingUpdate maxSurge 1 / maxUnavailable 0" app_strategy
check "http://app.$VM_IP.nip.io/ → 200 (/, /healthz, /readyz)" app_http

echo "Monitoring (AC-6, AC-7)"
check "Grafana reachable, admin login works" grafana_login
check "Prometheus target workload-api UP (2/2)" target_up 12
check "http_requests_total is queryable" requests_metric 12
check "http_request_duration_seconds is queryable" latency_metric 12

echo "Access from this machine"
if curl -s -o /dev/null --max-time 5 "http://app.$VM_IP.nip.io/"; then
  pass "*.nip.io resolves"
else
  warn "*.nip.io doesn't resolve here (not a cluster problem): see README → Troubleshooting"
fi

echo
if ((failed)); then echo "Some checks FAILED."; exit 1; fi
echo "All checks passed."
