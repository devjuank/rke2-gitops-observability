# rke2-gitops-observability

A local single-node **RKE2** cluster on a Multipass VM, running a small Go service observed with **Prometheus + Grafana**, delivered through **GitHub Actions + Argo CD**. Built as a take-home DevOps exercise.

> Work in progress: this README grows with each step of [`docs/PLAN.md`](docs/PLAN.md). Currently done: **step 1 (VM + RKE2)** and **step 2 (Go app)**.

## Prerequisites

Tested on macOS with Apple Silicon (arm64), 16 GB RAM.

| Tool | Install | Used for |
|---|---|---|
| [Multipass](https://canonical.com/multipass) | `brew install --cask multipass` | The Ubuntu VM that runs RKE2 |
| kubectl | `brew install kubectl` | Talking to the cluster from the host |
| make | Xcode Command Line Tools (`xcode-select --install`) | Entry point for every command |
| Docker with buildx | Docker Desktop | Building the multi-arch app image (step 3) |
| Go 1.27+ | Official installer from [go.dev/dl](https://go.dev/dl/) | Running the app's tests locally |

The VM needs 4 vCPU, 8 GB RAM and 40 GB of disk.

## Quick start

```sh
make up                              # create the VM, install RKE2 via cloud-init (~5–10 min)
export KUBECONFIG=$PWD/kubeconfig    # point kubectl at the cluster
make status                          # node and pods
```

`make up` launches the VM with [`infra/cloud-init.yaml`](infra/cloud-init.yaml), which installs a pinned RKE2 version and starts it. It then copies the cluster's admin kubeconfig to `./kubeconfig`, which is gitignored and points at the VM IP. Your `~/.kube/config` is not modified.

Run `make help` to list all targets.

## Verify

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

## Tear down

```sh
make down    # delete the VM and ./kubeconfig
```

## Docs

- [`docs/SPEC.md`](docs/SPEC.md): requirements and acceptance criteria
- [`docs/PLAN.md`](docs/PLAN.md): architecture and step-by-step plan
- [`docs/DECISIONS.md`](docs/DECISIONS.md): why each tool was chosen
