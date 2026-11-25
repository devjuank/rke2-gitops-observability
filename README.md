# rke2-gitops-observability

A local single-node **RKE2** cluster on a Multipass VM, running a small Go service observed with **Prometheus + Grafana**, delivered through **GitHub Actions + Argo CD**. Built as a take-home DevOps exercise.

> Work in progress: this README grows with each step of [`docs/PLAN.md`](docs/PLAN.md). Currently done: **steps 1–6**, the core of the exercise (VM + RKE2, Go app, image, app manifests, Prometheus + Grafana, dashboard).

## Prerequisites

Tested on macOS with Apple Silicon (arm64), 16 GB RAM.

To run the project:

| Tool | Install | Used for |
|---|---|---|
| [Multipass](https://canonical.com/multipass) | `brew install --cask multipass` | The Ubuntu VM that runs RKE2 |
| kubectl 1.35+ | `brew install kubectl` | Talking to the cluster (keep it within one minor version of RKE2 v1.36) |
| make | Xcode Command Line Tools (`xcode-select --install`) | Entry point for every command |

Only to develop the app (not needed to run it, the image is public):

| Tool | Install | Used for |
|---|---|---|
| Go 1.27+ | Official installer from [go.dev/dl](https://go.dev/dl/) | Running the app's tests locally |
| Docker with buildx | Docker Desktop | Building and publishing the multi-arch image |

The VM needs 4 vCPU, 8 GB RAM and 40 GB of disk.

## Quick start

```sh
make all       # VM + RKE2, monitoring + app, then the automated checks (~10–15 min)
```

`make all` runs these three, which you can also run one by one:

```sh
make up        # create the VM and install RKE2 via cloud-init (~5–10 min)
make deploy    # monitoring stack + app (~3–5 min); prints the URLs
make verify    # automated acceptance checks, ✔/✘ per check
```

That's all. To use `kubectl` yourself, point it at the cluster first: `export KUBECONFIG=$PWD/kubeconfig`.

`make up` launches the VM with [`infra/cloud-init.yaml`](infra/cloud-init.yaml), which installs a pinned RKE2 version and starts it. It then copies the cluster's admin kubeconfig to `./kubeconfig`, which is gitignored and points at the VM IP. Your `~/.kube/config` is not modified.

Run `make help` to list all targets.

### URLs

Everything is exposed through RKE2's Traefik ingress on the VM's IP, using [nip.io](https://nip.io) names (`<name>.<VM-IP>.nip.io` resolves to `<VM-IP>`). The VM IP differs per machine; `make deploy` prints the right URLs.

| Service | URL |
|---|---|
| App | `http://app.<VM-IP>.nip.io/` (also `http://<VM-IP>/`) |
| Grafana | `http://grafana.<VM-IP>.nip.io/` (user `admin`, password from `make grafana-password`) |
| Prometheus | `http://prometheus.<VM-IP>.nip.io/` |

Run `make urls` to print them again.

## Verify

`make verify` ([`scripts/verify.sh`](scripts/verify.sh)) checks everything that can be automated and exits non-zero if anything fails:

```
Cluster (AC-1)
  ✔ node Ready
  ✔ all pods Running or Completed
App (AC-2..AC-5)
  ✔ workload-api: 2/2 pods Ready
  ✔ readiness (/readyz) and liveness (/healthz) probes configured
  ✔ CPU/memory requests and limits set
  ✔ RollingUpdate maxSurge 1 / maxUnavailable 0
  ✔ http://app.<VM-IP>.nip.io/ → 200 (/, /healthz, /readyz)
Monitoring (AC-6, AC-7)
  ✔ Grafana reachable, admin login works
  ✔ Prometheus target workload-api UP (2/2)
  ✔ http_requests_total is queryable
  ✔ http_request_duration_seconds is queryable
Dashboard (AC-8)
  ✔ Grafana dashboard 'workload-api' loaded from its ConfigMap
Access from this machine
  ✔ *.nip.io resolves
```

The same checks by hand:

```sh
# Inside the VM
multipass exec rke2 -- cloud-init status            # status: done
multipass exec rke2 -- systemctl is-active rke2-server   # active

# From the host
kubectl get nodes -o wide                           # rke2 Ready, v1.36.6+rke2r2
kubectl get pods -A                                 # all Running or Completed
kubectl -n kube-system get pods | grep traefik      # ingress controller Running
```

Some `helm-install-*` pods may show a few restarts. That's expected: those jobs retry until their CRDs are registered, then end in `Completed`.

```sh
# App (step 4)
kubectl -n app get pods                             # 2/2 Running, READY 1/1
curl http://app.<VM-IP>.nip.io/                     # 200, JSON with the pod's hostname
```

```sh
# Monitoring (step 5)
kubectl -n monitoring get pods                      # all Running
make grafana-password                               # then log in to Grafana as admin
# Prometheus → Status → Targets: serviceMonitor/app/workload-api/0 shows 2/2 UP
```

Zero-downtime rollout check: run a request loop and restart the Deployment in another terminal. No request should fail.

```sh
while true; do curl -s -o /dev/null -w '%{http_code}\n' http://<VM-IP>/; done | sort | uniq -c   # Ctrl-C after the rollout
kubectl -n app rollout restart deploy/workload-api && kubectl -n app rollout status deploy/workload-api
```

## The app: `workload-api`

A small Go service ([`app/`](app/)) built to make probes and metrics easy to observe.

| Endpoint | Behavior |
|---|---|
| `/` | Always 200. JSON with the version and the pod's hostname |
| `/work` | Simulated workload: random latency (10 ms up to `WORK_MAX_LATENCY_MS`), returns 500 at `WORK_ERROR_RATE` |
| `/healthz` | Liveness: 200 whenever the process serves HTTP. It never checks dependencies |
| `/readyz` | Readiness: 503 during `STARTUP_DELAY_SECONDS` and after SIGTERM, 200 otherwise |
| `/metrics` | Prometheus: `http_requests_total{method,path,code}`, `http_request_duration_seconds{method,path}`, Go/process metrics |

| Env var | Default | Purpose |
|---|---|---|
| `PORT` | `8080` | Listen port |
| `WORK_MAX_LATENCY_MS` | `500` | Upper bound of `/work` latency |
| `WORK_ERROR_RATE` | `0.05` | Fraction of `/work` requests that fail (0–1) |
| `STARTUP_DELAY_SECONDS` | `0` | Simulated slow init, to show readiness gating |

On SIGTERM it marks itself not ready, keeps serving for 5 s so Kubernetes removes it from the Service endpoints, then drains in-flight requests (up to 10 s) and exits.

```sh
make test                              # go vet + go test -race
STARTUP_DELAY_SECONDS=10 make run      # run locally on :8080
make load URL=http://localhost:8080    # traffic to / and /work (Ctrl-C to stop)
docker build -t workload-api:dev app/  # multi-stage → distroless/static, non-root
```

### Container image

The image is public at `ghcr.io/devjuank/workload-api`, built for `linux/arm64` and `linux/amd64`, and tagged `sha-<commit>` (never `latest` in manifests). You don't need to build it to run this project.

To publish it yourself (maintainers only):

```sh
echo <PAT with write:packages> | docker login ghcr.io -u <user> --password-stdin
make image                                  # buildx → ghcr.io/devjuank/workload-api:sha-<commit>
docker buildx imagetools inspect ghcr.io/devjuank/workload-api:sha-$(git rev-parse --short HEAD)
```

GitHub creates new packages as private. After the first push, set it to public once: Package settings → Change visibility → Public.

### Kubernetes manifests

[`deploy/app/`](deploy/app/) is plain YAML assembled with Kustomize (`kubectl apply -k`). The image tag is pinned in `kustomization.yaml`.

- **Deployment:** 2 replicas; `RollingUpdate` with `maxSurge: 1`, `maxUnavailable: 0`, so a new pod must pass readiness before an old one is removed.
- **Probes:** readiness on `/readyz` (every 2 s, fails fast to take the pod out of the Service), liveness on `/healthz` (every 10 s, 3 failures before a restart).
- **Resources:** requests `50m` CPU / `64Mi`, limits `200m` / `128Mi`.
- **Security:** namespace enforces Pod Security `restricted`; the container runs as UID 65532 with a read-only root filesystem, no privilege escalation and all capabilities dropped.
- **Service + Ingress:** ClusterIP on port 80; a hostless Traefik Ingress, so it works whatever the VM IP is.

## Tear down

```sh
make down    # delete the VM and ./kubeconfig
```

## Monitoring

[`deploy/monitoring/kube-prometheus-stack.yaml`](deploy/monitoring/kube-prometheus-stack.yaml) is a `HelmChart` resource: RKE2's built-in helm-controller installs the chart (pinned version), so there's no `helm` CLI involved. `make deploy` applies it with the VM IP filled into the Grafana/Prometheus hostnames, waits for it, then deploys the app.

- **Prometheus** scrapes every 15 s and keeps 1 day of data, with no persistent storage. It discovers `ServiceMonitor`s in all namespaces; the app's lives in [`deploy/app/servicemonitor.yaml`](deploy/app/servicemonitor.yaml).
- **Grafana** comes with the chart's default Kubernetes dashboards. Its admin password is random, created by `make deploy` as a Kubernetes Secret, and never stored in Git.
- **Not deployed:** Alertmanager (alerting is out of scope) and the etcd / scheduler / controller-manager / kube-proxy monitors (on RKE2 they listen only on `127.0.0.1`, so they would always show as DOWN).

## Dashboard

The **workload-api** dashboard (Grafana → Dashboards) is code: [`deploy/monitoring/dashboards/workload-api.json`](deploy/monitoring/dashboards/workload-api.json), packed by Kustomize into a ConfigMap labeled `grafana_dashboard: "1"` that Grafana's sidecar loads automatically.

| Row | Panels |
|---|---|
| At a glance | Request rate · Error rate · p95 latency of `/work` · Ready pods |
| Traffic (RED) | Request rate by path · Error rate (% 5xx) |
| Latency and stability | `/work` latency p50 / p95 / p99 · Container restarts |
| Resources | CPU per pod vs request/limit · Memory per pod vs request/limit |

Probe traffic (`/healthz`, `/readyz`) is excluded from the request and error panels so they reflect user traffic. To see it move:

```sh
make load      # traffic to / and /work against the cluster; Ctrl-C to stop
```

**Known limitation:** the p99 of `/work` can read above `WORK_MAX_LATENCY_MS` (e.g. ~0.69 s with a 0.5 s max). The app uses the client library's default histogram buckets, which jump from 0.5 s to 1 s, and `histogram_quantile` interpolates linearly inside that bucket. The fix (buckets matched to the workload's range) is planned as the first change shipped through the CI/CD pipeline.

To change the dashboard, edit it in Grafana, export the JSON (Share → Export), replace the file and run `make deploy`.

## Troubleshooting

**`*.nip.io` doesn't resolve** (`curl: (6) Could not resolve host`). Some routers drop public DNS answers that point to private IPs (DNS rebinding protection). `make deploy` detects this. One-time fix on macOS, which sends only `*.nip.io` lookups to Cloudflare's DNS and leaves everything else unchanged:

```sh
sudo mkdir -p /etc/resolver
echo "nameserver 1.1.1.1" | sudo tee /etc/resolver/nip.io
```

Test with `curl` or a browser (`dig` ignores `/etc/resolver`). Undo with `sudo rm /etc/resolver/nip.io`. Alternatively, add the names to `/etc/hosts`.

## Docs

- [`docs/SPEC.md`](docs/SPEC.md): requirements and acceptance criteria
- [`docs/PLAN.md`](docs/PLAN.md): architecture and step-by-step plan
- [`docs/DECISIONS.md`](docs/DECISIONS.md): why each tool was chosen
