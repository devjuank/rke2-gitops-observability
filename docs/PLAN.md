# rke2-gitops-observability — Implementation Plan

> The requirements and acceptance criteria (the *what*) are in `SPEC.md`. This file is the *how*. The *why* is in `DECISIONS.md`.

## Goal

Build a small, reproducible **local Kubernetes environment with RKE2**. Deploy a **Go service** to it and observe the service with **Prometheus and Grafana**. The priority is a clear, working solution with well-reasoned decisions, not production-readiness.

## Architecture (overview)

```
 macOS (Apple Silicon, M2 Pro)
 ├── make up / make deploy / make down
 ├── kubectl  (kubeconfig copied from the VM)
 └── Browser → app / Grafana / Argo CD via <name>.<VM-IP>.nip.io
        │
        ▼
 Multipass VM — Ubuntu 24.04 (arm64), 4 vCPU / 8 GB RAM / 40 GB disk
 └── RKE2 single-node (server = control plane + worker)
     ├── bundled: containerd, Canal CNI, CoreDNS, Traefik ingress, metrics-server
     ├── HelmChart CRs (kubectl apply by make deploy) ← reconciled by RKE2's helm-controller
     │    ├── kube-prometheus-stack (HelmChart)
     │    └── argo-cd (HelmChart)
     ├── ns: monitoring → Prometheus, Grafana, node-exporter, kube-state-metrics (no Alertmanager)
     ├── ns: argocd     → Argo CD (syncs deploy/app from GitHub)
     └── ns: app        → Go service (Deployment, Service, Ingress, ServiceMonitor)

 GitHub
 ├── repo: source, manifests, README
 ├── Actions: checks → build multi-arch image → push → deploy PR bumping the image tag in deploy/app
 └── ghcr.io: container image (public)

 CI/CD flow (pull-based GitOps):
 PR merged (app/) → Actions: checks + build + push ghcr.io:<sha> → deploy PR with the new tag in deploy/app/kustomization.yaml
                  → checks pass → auto-merge → Argo CD (inside the VM) detects the Git change → syncs → rolling update
```

## Decided components

| Area | Decision |
|---|---|
| Linux environment | **Multipass** VM (Ubuntu 24.04 arm64) on the Mac |
| VM lifecycle | **Makefile** wrapping `multipass launch --cloud-init` |
| Node provisioning | **cloud-init** installs and configures RKE2 at first boot |
| Kubernetes | **RKE2 single-node** (v1.36) with the bundled Traefik ingress |
| App | Small **Go** HTTP service: `/`, `/healthz`, `/readyz`, `/metrics` |
| Image registry | **GitHub Container Registry** (ghcr.io), public, multi-arch |
| App manifests | **Kustomize** (plain YAML, built into kubectl) |
| Monitoring | **kube-prometheus-stack** as an RKE2 `HelmChart` manifest |
| App scraping | **ServiceMonitor** (Prometheus Operator) |
| Dashboard | Grafana dashboard JSON in a **ConfigMap** (sidecar auto-loads it) |
| Delivery | **Argo CD** Application → `deploy/app` (auto-sync + self-heal) |
| Access from Mac | **Ingress** (hostless for the app) + **nip.io** hostnames on the VM IP for Grafana/Argo CD |
| CI/CD | **GitHub Actions** (checks → multi-arch build → push to ghcr.io → deploy PR with the new tag in `deploy/app`, auto-merged on a protected `main`) + **Argo CD** pulls and deploys |

## Naming

- **Repository:** `rke2-gitops-observability`. It's a neutral, descriptive name, so it can live on as a public portfolio project.
- **Go service / image:** `workload-api` → `ghcr.io/<user>/workload-api`

## Repository layout

```
rke2-gitops-observability/
├── Makefile                      # up, kubeconfig, deploy, status, down
├── README.md                     # how to run, architecture, decisions
├── infra/
│   └── cloud-init.yaml           # RKE2 config + install + enable
├── app/
│   ├── main.go                   # HTTP server, probes, Prometheus metrics, graceful shutdown
│   ├── main_test.go
│   ├── go.mod
│   └── Dockerfile                # multi-stage, distroless/static, non-root
├── deploy/
│   ├── app/                      # Kustomize: namespace, deployment, service, ingress (+ servicemonitor in step 5)
│   ├── monitoring/               # kube-prometheus-stack HelmChart + dashboard ConfigMap
│   └── argocd/                   # Argo CD HelmChart + Application
├── docs/
│   ├── PLAN.md                   # this file
│   └── DECISIONS.md              # why we chose / discarded each option
└── .github/workflows/ci.yml
```

## Steps / checklist

### 1. VM + RKE2 (Makefile + cloud-init)
- [x] `brew install multipass` (one-time, outside the Makefile)
- [x] `infra/cloud-init.yaml`:
  - [x] write `/etc/rancher/rke2/config.yaml` (`write-kubeconfig-mode: "0644"`, `tls-san` with the VM IP/hostname)
  - [x] `curl -sfL https://get.rke2.io | sh -` then `systemctl enable --now rke2-server`
  - [x] symlink `kubectl` (`/var/lib/rancher/rke2/bin`) into PATH
- [x] `make up` → `multipass launch 24.04 --name rke2 --cpus 4 --memory 8G --disk 40G --cloud-init infra/cloud-init.yaml`
- [x] `make kubeconfig` → copy `/etc/rancher/rke2/rke2.yaml` to the Mac, replace `127.0.0.1` with the VM IP
- [x] Verify: `kubectl get nodes`, `kubectl get pods -A`

### 2. Go application (own small service, not an open-source app)
- [x] Endpoints:
  - `/` — sample response (fast, always 200)
  - `/work` — **simulated workload** with **random latency** (e.g. 10–500 ms) and a **configurable error rate** (e.g. 5% → HTTP 500), so the dashboard shows real latency percentiles and errors during the demo
  - `/healthz` — liveness: always 200 if the HTTP server responds
  - `/readyz` — readiness: 200 only after init, 503 during startup/shutdown
  - `/metrics` — Prometheus metrics
- [x] Config via env vars (set in the Deployment, so they're changeable through Git/Argo CD): `PORT`, `WORK_MAX_LATENCY_MS`, `WORK_ERROR_RATE`, `STARTUP_DELAY_SECONDS` (simulates slow init to show readiness in action)
- [x] Metrics with `prometheus/client_golang`: `http_requests_total{method,path,code}` counter, `http_request_duration_seconds{method,path}` histogram, plus default Go/process metrics
- [x] Load generator for the demo: `make load` (e.g. `hey` or a small loop with `curl` against `/` and `/work`), or an optional in-cluster `Job`
- [x] Structured logs (`log/slog`), graceful shutdown on SIGTERM (mark not-ready → drain → exit)
- [x] Table-driven tests for the handlers
- [x] Multi-stage Dockerfile → `gcr.io/distroless/static:nonroot`

### 3. Image
- [x] `docker buildx build --platform linux/arm64,linux/amd64 -t ghcr.io/<user>/workload-api:<tag> --push app/`
- [x] Make the ghcr.io package public (no imagePullSecret needed)

### 4. App manifests (Kustomize, namespace `app`)
- [x] Deployment strategy: `RollingUpdate` with `maxSurge: 1`, `maxUnavailable: 0` (a new pod must pass readiness before an old one is removed → zero downtime)
- [x] Deployment: 2 replicas, `livenessProbe` → `/healthz`, `readinessProbe` → `/readyz`, requests/limits (e.g. 50m/64Mi → 200m/128Mi), securityContext (non-root, read-only rootfs)
- [x] Service (ClusterIP, named port `http`)
- [x] Ingress (`ingressClassName: traefik`, no `host`, so it works for any VM IP)

### 5. Monitoring
- [x] `HelmChart` for kube-prometheus-stack (namespace `monitoring`), applied by `make deploy`
- [x] ServiceMonitor for `workload-api` added to `deploy/app` (its CRD only exists after this step)
- [x] Values: Grafana + Prometheus Ingress (`grafana.` / `prometheus.<VM-IP>.nip.io`), ServiceMonitor discovery across namespaces, modest resource requests, Alertmanager off
- [x] Verify the target shows **UP** in Prometheus → Status → Targets

### 6. Grafana dashboard (as code)
- [x] ConfigMap labeled `grafana_dashboard: "1"` with the dashboard JSON
- [x] Panels:
  - Request rate (RPS) by path
  - Error rate (% 5xx)
  - Latency p95 / p99 (histogram_quantile)
  - Pod CPU vs request/limit
  - Pod memory vs request/limit
  - Container restarts

### 7a. CI checks (GitHub Actions, validation only)
- [x] `.github/workflows/checks.yml` on every push and pull request, `permissions: contents: read`
- [x] Go: `gofmt`, `go vet`, `go test -race`
- [x] Manifests: `kubectl kustomize` + kubeconform (Kubernetes 1.36 + CRD schemas); dashboard JSON well-formed
- [x] `shellcheck scripts/*.sh`; `docker build` without push
- [x] Negative test: a broken manifest on a branch makes the workflow fail

### 7. Argo CD (GitOps)
- [x] `HelmChart` for argo-cd (namespace `argocd`)
- [x] `Application` → repo `deploy/app`, `automated: {prune: true, selfHeal: true}`
- [x] Demo: change replicas/image tag in Git → cluster converges automatically (`WORK_ERROR_RATE` 0.05 → 0.10)

### 8. CI/CD (GitHub Actions + Argo CD)
- [x] `main` protected by a ruleset: PR required, `checks` jobs required, squash only, no force-push/deletion, no bypass
- [x] Trigger: push to `main` with changes under `app/` (`paths: ["app/**"]`), so the deploy PR (only `deploy/app`) doesn't retrigger the build
- [x] Job 1 — checks: reuse `checks.yml` (`workflow_call`)
- [x] Job 2 — build & push: `docker/setup-buildx-action` (no QEMU: the Dockerfile cross-compiles), `docker/login-action` to ghcr.io with `GITHUB_TOKEN`, `docker/build-push-action` for `linux/arm64,linux/amd64`, tags `sha-<short>` and `latest`
- [x] Job 3 — deploy PR: `kustomize edit set image` in `deploy/app`, open a PR as a GitHub App (`actions/create-github-app-token`) with auto-merge
- [x] Permissions: `packages: write` for the image (`GITHUB_TOKEN`); App token (Contents + Pull requests) for the deploy PR
- [x] Argo CD auto-sync picks up the new tag (default polling ~3 min; `argocd app sync` or a refresh from the UI makes it instant for the demo)
- [x] First change shipped through the pipeline: histogram buckets matched to `/work`'s 10–500 ms range (the default buckets jump from 0.5 s to 1 s, so p99 is over-estimated; see README → Dashboard)
- [x] Never deploy `latest`: the manifest always pins the immutable `sha-` tag, so every deploy is traceable to a commit and easy to roll back (`git revert`)

### 9. README
- [x] Prerequisites and `make up && make deploy`
- [x] Architecture diagram and component overview
- [x] Key decisions and assumptions (summary of `DECISIONS.md`)
- [x] How to verify (URLs, commands)
- [x] What I'd do for production (next steps)

## Acceptance criteria (definition of done per step)

A step is done only when its checks pass. Run them, paste the output and commit.

| Step | Acceptance criteria | How to verify |
|---|---|---|
| 1. VM + RKE2 | Node `Ready`; all `kube-system` pods `Running`/`Completed`; `kubectl` works from the Mac | `kubectl get nodes -o wide`, `kubectl get pods -A` |
| 2. Go app | `go test -race ./...` passes; locally `/` → 200, `/work` returns 200/500 with variable latency, `/healthz` → 200, `/readyz` → 503 during `STARTUP_DELAY_SECONDS` then 200, `/metrics` exposes `http_requests_total` and `http_request_duration_seconds` | `go test`, `curl -i localhost:8080/...` |
| 3. Image | Multi-arch image (arm64 + amd64) public on ghcr.io | `docker buildx imagetools inspect ghcr.io/<user>/workload-api:<tag>` |
| 4. App manifests | 2/2 pods `Ready` in `app`; `curl http://app.<VM-IP>.nip.io/` → 200 from the Mac; probes and requests/limits present; `kubectl rollout restart` causes **no failed requests** while a load loop runs | `kubectl -n app get pods`, `kubectl -n app describe deploy`, curl loop during rollout |
| 5. Monitoring | All `monitoring` pods `Running`; Grafana reachable at `http://grafana.<VM-IP>.nip.io`; Prometheus target for `workload-api` is **UP** | Prometheus UI → Status → Targets, or `up{job=~".*workload-api.*"} == 1` |
| 6. Dashboard | Dashboard auto-loaded from the ConfigMap (no manual import); all 6 panels show data while `make load` runs | Grafana UI |
| 7a. CI checks | `checks` workflow green on `main`; a broken manifest on a branch turns it red | `gh run list --workflow checks.yml`, Actions tab |
| 7. Argo CD | Application `Synced` + `Healthy`; a manual `kubectl scale` is reverted by self-heal; a Git change to `deploy/app` is applied without `kubectl` | `kubectl -n argocd get applications`, Argo CD UI |
| 8. CI/CD | PR touching `app/` merged → release green → bot's deploy PR bumps the `sha-` tag and auto-merges → Argo CD rolls out the new image (< ~5 min, or instant with Refresh); no workflow loop | GitHub Actions UI, `kubectl -n app get deploy -o jsonpath='{..image}'` |
| 9. README | A fresh reader can reproduce with `make up && make deploy` following only the README | Read it top to bottom on a clean VM (`make down && make up`) |
| End-to-end | `make down && make up && make deploy` rebuilds everything from zero with no manual steps besides the documented prerequisites | Full rebuild before submitting |

## Order of work

Priority: what the exercise explicitly asks for comes first. Extras come after.

1. VM + RKE2
2. Go app + image
3. App manifests (probes, resources, Ingress)
4. Prometheus + Grafana + ServiceMonitor
5. Dashboard
6. README
7. CI checks *(extra, validation only)*
8. Argo CD *(extra)*
9. GitHub Actions CI/CD with deploy PRs → Argo CD *(extra, builds on Argo CD)*

## Production next steps (for the README)

- 3 control-plane nodes (etcd quorum) + dedicated workers, provisioned with **Terraform** (Azure VMs) and bootstrapped with the same cloud-init
- **Ansible** for OS-level day-2 operations (patching, hardening, users, firewall)
- Progressive delivery with **Argo Rollouts**: canary (e.g. 20% → 50% → 100%) with automated analysis against Prometheus (error rate, p95 latency) and automatic rollback; blue-green for changes that need an instant switch
- RKE2 upgrades with Rancher's **System Upgrade Controller** (declarative, node by node, cordon/drain)
- Persistent storage for Prometheus/Grafana (Longhorn or a CSI driver), retention policies, remote write / Thanos for long-term storage
- Alerting rules (SLO-based) routed via Alertmanager; logs with Loki or Elasticsearch
- Images pinned by digest, signed and scanned (Trivy) in CI; secrets via Sealed Secrets / External Secrets
- TLS with cert-manager; NetworkPolicies; Pod Security Admission
