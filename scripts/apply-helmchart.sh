#!/usr/bin/env bash
# Applies an RKE2 HelmChart (or a HelmChartConfig, which overrides one of RKE2's
# bundled charts) and waits until helm-controller has (re)installed it, then
# until every Deployment in <namespace> is Available.
# Usage: apply-helmchart.sh <file> <namespace>
# The chart name is read from the file; __VM_IP__ is replaced with $VM_IP.
set -euo pipefail

file=$1 namespace=$2
: "${VM_IP:?VM_IP is not set (run via make)}"
name=$(awk '/^metadata:/{m=1} m && /^  name:/{print $2; exit}' "$file")
job="helm-install-$name"

sed "s/__VM_IP__/$VM_IP/g" "$file" | kubectl apply -f -
echo "Waiting for helm-controller to install $name (first run: a few minutes)..."

# When values change, helm-controller replaces the install job. Give it a
# moment so we don't wait on the previous, already-completed job.
sleep 5
for _ in $(seq 60); do kubectl -n kube-system get job "$job" >/dev/null 2>&1 && break; sleep 5; done
kubectl -n kube-system wait --for=condition=complete "job/$job" --timeout=600s
kubectl -n "$namespace" wait --for=condition=Available deploy --all --timeout=600s
