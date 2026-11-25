# rke2-gitops-observability

A local single-node **RKE2** cluster on a Multipass VM, running a small Go service observed with **Prometheus + Grafana**, delivered through **GitHub Actions + Argo CD**. Built as a take-home DevOps exercise.

> Work in progress: this README grows with each step of [`docs/PLAN.md`](docs/PLAN.md). Currently done: **step 1 (VM + RKE2)**.

## Prerequisites

Tested on macOS with Apple Silicon (arm64), 16 GB RAM.

| Tool | Install | Used for |
|---|---|---|
| [Multipass](https://canonical.com/multipass) | `brew install --cask multipass` | The Ubuntu VM that runs RKE2 |
| kubectl | `brew install kubectl` | Talking to the cluster from the host |
| make | Xcode Command Line Tools (`xcode-select --install`) | Entry point for every command |
| Docker with buildx | Docker Desktop | Building the multi-arch app image (step 3) |
| Go | `brew install go` | Running the app's tests locally (step 2) |

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

## Tear down

```sh
make down    # delete the VM and ./kubeconfig
```

## Docs

- [`docs/SPEC.md`](docs/SPEC.md): requirements and acceptance criteria
- [`docs/PLAN.md`](docs/PLAN.md): architecture and step-by-step plan
- [`docs/DECISIONS.md`](docs/DECISIONS.md): why each tool was chosen
