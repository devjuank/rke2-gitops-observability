# Specification — rke2-gitops-observability

> **What** must be built and **how we know it's done**. The *how* lives in `PLAN.md`, the *why* in `DECISIONS.md`.
> Source: take-home DevOps exercise (requirements paraphrased from the assignment).

## 1. Context

The exercise is built around **Kubernetes on RKE2**, **Go services**, **Prometheus** and **Grafana**. It evaluates how a DevOps problem is approached: the **technical decisions** and how clearly they are **communicated**. It is **not** expected to be production-ready. Expected effort is 5–10 hours.

## 2. Functional requirements

| ID | Requirement | Source |
|---|---|---|
| FR-1 | Set up a **single-node RKE2** Kubernetes environment on a reasonable Linux environment (host machine is macOS). | Assignment |
| FR-2 | **Containerize** a Go backend (own or open source) and **deploy** it to the cluster. | Assignment |
| FR-3 | Make the application **accessible from the host machine**. | Assignment |
| FR-4 | Add appropriate **health and readiness checks**. | Assignment |
| FR-5 | Add **basic resource configuration** (requests/limits). | Assignment |
| FR-6 | Deploy **Prometheus** and **Grafana** to the cluster. | Assignment |
| FR-7 | Configure Prometheus to **collect metrics from the application**. | Assignment |
| FR-8 | Create a **Grafana dashboard** with a few useful application and/or infrastructure metrics. | Assignment |
| FR-9 | Provide a **README** covering: how to reproduce/run, architecture overview, important technical decisions and assumptions. | Assignment |
| FR-10 | Deliver the **repository + README** before the walkthrough, and give a **short walkthrough** of the solution. | Assignment |

### Additional scope (own choices, beyond the assignment)

| ID | Requirement | Rationale |
|---|---|---|
| EX-1 | The Go service exposes a **simulated workload** (`/work`) with random latency and a configurable error rate. | Makes the dashboard meaningful (latency percentiles, errors) |
| EX-2 | **GitOps delivery** with Argo CD: the cluster state comes from Git, with auto-sync and self-heal. | Makes delivery declarative, auditable and self-correcting |
| EX-3 | **CI/CD**: on code change, CI tests, builds and publishes a multi-arch image and updates the image tag in Git, and Argo CD deploys it. | End-to-end delivery without CI reaching the cluster |
| EX-4 | **Zero-downtime** rollouts (readiness-gated rolling update). | Shows probes and deployments working together |

## 3. Non-functional requirements

| ID | Requirement |
|---|---|
| NFR-1 | **Reproducible from zero** with documented prerequisites and a few commands (`make up && make deploy`). No undocumented manual steps. |
| NFR-2 | **Everything as code**: VM provisioning, cluster add-ons, app manifests, dashboard and pipeline are versioned in the repo. |
| NFR-3 | Runs on an **Apple Silicon (arm64)** laptop with 16 GB RAM; the app image is also **amd64**-compatible. |
| NFR-4 | **Simple and readable** over feature-rich: plain, commented YAML; no components beyond the plan. |
| NFR-5 | **Basic security hygiene**: no secrets in Git, non-root containers, read-only root filesystem, least-privilege CI permissions, immutable image tags (no `latest` in manifests). |
| NFR-6 | Documentation in **English**. |

## 4. Out of scope (non-goals)

- High availability (multi-node control plane), backups/DR and persistent storage for Prometheus/Grafana.
- TLS certificates, external DNS and public exposure of the cluster.
- Alerting/on-call routing, log aggregation and tracing.
- Canary/blue-green progressive delivery (documented as a next step).
- Multi-environment promotion (dev/staging/prod).

These are covered in the README's **"Production next steps"**.

## 5. Assumptions

- The reviewer has a macOS or Linux machine with Multipass (or can adapt the cloud-init to any Ubuntu VM).
- The GitHub repository and the ghcr.io image are **public**, so no credentials are needed to pull.
- `nip.io` is reachable for hostname resolution, or `/etc/hosts` can be used as a fallback.
- Chart and RKE2 versions are pinned to the versions tested during development.

## 6. Acceptance criteria

Traceability from requirements to verifiable checks. The detailed commands are in `PLAN.md` → *Acceptance criteria*.

| ID | Covers | Done when |
|---|---|---|
| AC-1 | FR-1 | The RKE2 node is `Ready` and all system pods are `Running`/`Completed`; `kubectl` works from the host. |
| AC-2 | FR-2, NFR-3 | A multi-arch image (arm64 + amd64) is published and the `workload-api` Deployment has 2/2 pods `Ready`. |
| AC-3 | FR-3 | `curl http://app.<VM-IP>.nip.io/` from the host returns HTTP 200. |
| AC-4 | FR-4 | `/healthz` returns 200; `/readyz` returns 503 during startup delay and shutdown, then 200; both probes are configured in the Deployment. |
| AC-5 | FR-5 | Requests and limits are set on the app container (and kept modest on monitoring components). |
| AC-6 | FR-6 | Prometheus and Grafana are running; Grafana is reachable from the host. |
| AC-7 | FR-7 | The `workload-api` target is **UP** in Prometheus, and `http_requests_total` / `http_request_duration_seconds` are queryable. |
| AC-8 | FR-8, EX-1 | A dashboard loaded from code shows request rate, error %, p95/p99 latency, CPU and memory vs requests/limits, and restarts, with data while load runs. |
| AC-9 | FR-9 | Following only the README, a fresh environment is reproduced and verified. |
| AC-10 | EX-2 | The Argo CD Application is `Synced`/`Healthy`; manual drift is reverted; Git changes apply without `kubectl`. |
| AC-11 | EX-3 | A push under `app/` leads to the new image running in the cluster with no manual step and no CI loop. |
| AC-12 | EX-4 | A rollout under continuous load produces no failed requests. |
| AC-13 | NFR-1, NFR-2 | `make down && make up && make deploy` rebuilds everything from zero. |
| AC-14 | FR-10 | Repo link and README sent at least 30 minutes before the walkthrough. |
