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
     ├── ns: monitoring → Prometheus, Grafana, Alertmanager, node-exporter, kube-state-metrics
     ├── ns: argocd     → Argo CD (syncs deploy/app from GitHub)
     └── ns: app        → Go service (Deployment, Service, Ingress, ServiceMonitor)

 GitHub
 ├── repo: source, manifests, README
 ├── Actions: test → build multi-arch image → push → bump image tag in deploy/app (commit)
 └── ghcr.io: container image (public)

 CI/CD flow (pull-based GitOps):
 git push (app/) → Actions: test + build + push ghcr.io:<sha> → commit new tag to deploy/app/kustomization.yaml
                 → Argo CD (inside the VM) detects the Git change → syncs → rolling update
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
| CI/CD | **GitHub Actions** (test → multi-arch build → push to ghcr.io → commit new tag to `deploy/app`) + **Argo CD** pulls and deploys |

## Naming

- **Repository:** `rke2-gitops-observability`. It's a neutral name with no company branding, so it can live on as a public portfolio project. The README says "Built as a take-home exercise" without naming the company.
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
- [ ] `docker buildx build --platform linux/arm64,linux/amd64 -t ghcr.io/<user>/workload-api:<tag> --push app/`
- [ ] Make the ghcr.io package public (no imagePullSecret needed)

### 4. App manifests (Kustomize, namespace `app`)
- [ ] Deployment strategy: `RollingUpdate` with `maxSurge: 1`, `maxUnavailable: 0` (a new pod must pass readiness before an old one is removed → zero downtime)
- [ ] Deployment: 2 replicas, `livenessProbe` → `/healthz`, `readinessProbe` → `/readyz`, requests/limits (e.g. 50m/64Mi → 200m/128Mi), securityContext (non-root, read-only rootfs)
- [ ] Service (ClusterIP, named port `http`)
- [ ] Ingress (`ingressClassName: traefik`, no `host`, so it works for any VM IP)

### 5. Monitoring
- [ ] `HelmChart` for kube-prometheus-stack (namespace `monitoring`), applied by `make deploy`
- [ ] ServiceMonitor for `workload-api` added to `deploy/app` (its CRD only exists after this step)
- [ ] Values: Grafana Ingress (`grafana.<VM-IP>.nip.io`), ServiceMonitor discovery across namespaces, modest resource requests
- [ ] Verify the target shows **UP** in Prometheus → Status → Targets

### 6. Grafana dashboard (as code)
- [ ] ConfigMap labeled `grafana_dashboard: "1"` with the dashboard JSON
- [ ] Panels:
  - Request rate (RPS) by path
  - Error rate (% 5xx)
  - Latency p95 / p99 (histogram_quantile)
  - Pod CPU vs request/limit
  - Pod memory vs request/limit
  - Container restarts

### 7. Argo CD (GitOps)
- [ ] `HelmChart` for argo-cd (namespace `argocd`)
- [ ] `Application` → repo `deploy/app`, `automated: {prune: true, selfHeal: true}`
- [ ] Demo: change replicas/image tag in Git → cluster converges automatically

### 8. CI/CD (GitHub Actions + Argo CD)
- [ ] Trigger: push to `main` with changes under `app/` (`paths: ["app/**"]`), so the bot's own commit to `deploy/` doesn't retrigger the build
- [ ] Job 1 — test: `go vet`, `go test -race`
- [ ] Job 2 — build & push: `docker/setup-qemu-action` + `docker/setup-buildx-action`, `docker/login-action` to ghcr.io with `GITHUB_TOKEN`, `docker/build-push-action` for `linux/arm64,linux/amd64`, tags `sha-<short>` and `latest`
- [ ] Job 3 — bump: `kustomize edit set image ghcr.io/<user>/workload-api=ghcr.io/<user>/workload-api:sha-<short>` in `deploy/app`, commit and push as `github-actions[bot]`
- [ ] Workflow permissions: `contents: write` (bump commit), `packages: write` (ghcr.io push)
- [ ] Argo CD auto-sync picks up the new tag (default polling ~3 min; `argocd app sync` or a refresh from the UI makes it instant for the demo)
- [ ] Never deploy `latest`: the manifest always pins the immutable `sha-` tag, so every deploy is traceable to a commit and easy to roll back (`git revert`)

### 9. README
- [ ] Prerequisites and `make up && make deploy`
- [ ] Architecture diagram and component overview
- [ ] Key decisions and assumptions (summary of `DECISIONS.md`)
- [ ] How to verify (URLs, commands)
- [ ] What I'd do for production (next steps)

### 10. Walkthrough prep
- [ ] Environment running before the call; tabs open: terminal (`kubectl get pods -A`), app, Prometheus targets, Grafana dashboard, Argo CD
- [ ] CD demo: change the response in `app/main.go` → push → Actions builds and bumps the tag → Argo CD syncs → new pods roll out with no downtime (readiness gates the traffic) → `curl` shows the new response
- [ ] Short live demo: `make load` → dashboard moves (RPS, p95/p99, error %); raise `WORK_ERROR_RATE` in Git → Argo CD syncs → error panel climbs; restart a pod with `STARTUP_DELAY_SECONDS` → it stays out of the Service until `/readyz` is 200
- [ ] Send repo + README to the reviewer at least 30 min before the meeting

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
| 7. Argo CD | Application `Synced` + `Healthy`; a manual `kubectl scale` is reverted by self-heal; a Git change to `deploy/app` is applied without `kubectl` | `kubectl -n argocd get applications`, Argo CD UI |
| 8. CI/CD | Push to `app/` → workflow green → bot commit bumps the `sha-` tag → Argo CD rolls out the new image (< ~5 min, or instant with Refresh); no workflow loop | GitHub Actions UI, `kubectl -n app get deploy -o jsonpath='{..image}'` |
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
7. Argo CD *(extra)*
8. GitHub Actions CI/CD with tag bump → Argo CD *(extra, builds on step 7)*

## Production next steps (for the README)

- 3 control-plane nodes (etcd quorum) + dedicated workers, provisioned with **Terraform** (Azure VMs) and bootstrapped with the same cloud-init
- **Ansible** for OS-level day-2 operations (patching, hardening, users, firewall)
- Progressive delivery with **Argo Rollouts**: canary (e.g. 20% → 50% → 100%) with automated analysis against Prometheus (error rate, p95 latency) and automatic rollback; blue-green for changes that need an instant switch
- RKE2 upgrades with Rancher's **System Upgrade Controller** (declarative, node by node, cordon/drain)
- Persistent storage for Prometheus/Grafana (Longhorn or a CSI driver), retention policies, remote write / Thanos for long-term storage
- Alerting rules (SLO-based) routed via Alertmanager; logs with Loki or Elasticsearch
- Images pinned by digest, signed and scanned (Trivy) in CI; secrets via Sealed Secrets / External Secrets
- TLS with cert-manager; NetworkPolicies; Pod Security Admission
