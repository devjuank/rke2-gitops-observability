# CLAUDE.md — rke2-gitops-observability

Guidance for Claude Code in this repository. Read it fully before doing anything.

## What this project is

A take-home DevOps exercise: a **local single-node RKE2 cluster** on a Multipass VM, running a small **Go service** (`workload-api`), observed with **Prometheus + Grafana**, delivered through **GitHub Actions + Argo CD (GitOps)**. Reviewers care about **approach, technical decisions and clarity**, not production-readiness.

## Source of truth (read first, every session)

1. `docs/SPEC.md`: **what** must be built (requirements FR/EX/NFR, out of scope, assumptions) and the **acceptance criteria** (AC-x). This is the contract.
2. `docs/PLAN.md`: **how**: architecture, step checklist and the verification commands for each acceptance criterion.
3. `docs/DECISIONS.md`: **why** each tool was chosen and what was discarded.

If the plan and the spec ever conflict, the spec wins: flag the conflict to me. Reference requirement and AC IDs in commit messages when relevant (e.g. `feat(app): readiness gating on startup (FR-4, AC-4)`).

**Do not change a decision in `DECISIONS.md` without asking me first.** If something in the plan doesn't work as expected (version, chart value, API change), stop, explain the problem and propose options. Don't silently switch tools.

## Environment

- Host: macOS, Apple Silicon **M2 Pro (arm64)**, 16 GB RAM.
- VM: Multipass, name **`rke2`**, Ubuntu 24.04 arm64, 4 vCPU / 8 GB / 40 GB.
- Cluster: RKE2 single node; kubeconfig copied to the Mac by `make kubeconfig`.
- Registry: `ghcr.io/<github-user>/workload-api`, public, multi-arch (`linux/arm64,linux/amd64`).
- Access: Ingress (RKE2's bundled Traefik, `ingressClassName: traefik`). The app Ingress is hostless; Grafana/Argo CD use `nip.io` hosts on the VM IP.
- Namespaces: `app`, `monitoring`, `argocd`.

## How to work

- **Spec-driven, one step at a time.** Follow the order in `docs/PLAN.md`. For each step:
  1. Briefly restate the step and its acceptance criteria, and propose the files you'll create or change. Wait for my OK.
  2. Implement.
  3. Give me the exact **verification commands** from the acceptance criteria. Commands that run inside the VM use `multipass exec rke2 -- ...`.
  4. When the checks pass, tick the checkbox in `docs/PLAN.md` and make **one commit per step**.
- Prefer the **simplest working solution**. Don't add components that aren't in the plan. Extras (Argo CD, CI/CD) come only after steps 1–6 work.
- If a command fails, show the relevant error output, explain the cause in one or two lines and fix it. Don't loop on retries.
- Keep the repo **reproducible from zero**: everything goes through the `Makefile`, `infra/cloud-init.yaml`, manifests or the workflow. No manual-only steps unless they're documented in the README as prerequisites.

## Conventions

- **Commits:** Conventional Commits, small and descriptive, e.g. `feat(infra): cloud-init installs RKE2`, `feat(app): add /work endpoint with configurable errors`, `docs: architecture section`.
- **Go:** idiomatic and small; `net/http` + `prometheus/client_golang`; `log/slog`; `context`; graceful shutdown (on SIGTERM: mark not-ready → short wait → drain → exit); table-driven tests; `gofmt`/`go vet` clean. Config via env vars: `PORT`, `WORK_MAX_LATENCY_MS`, `WORK_ERROR_RATE`, `STARTUP_DELAY_SECONDS`.
- **Probes:** `/healthz` (liveness) never checks dependencies. `/readyz` (readiness) returns 503 until init completes and during shutdown.
- **Dockerfile:** multi-stage, `CGO_ENABLED=0`, final image `gcr.io/distroless/static:nonroot`, non-root user.
- **Kubernetes YAML:** plain, readable, with short comments explaining non-obvious choices. Kustomize for the app. Pin image tags to `sha-<short>`, never `latest`, in `deploy/app`. Always set resource requests/limits, securityContext (non-root, read-only root fs, no privilege escalation) and a `RollingUpdate` strategy with `maxSurge: 1`, `maxUnavailable: 0`.
- **RKE2 add-ons:** kube-prometheus-stack and Argo CD are declared as `HelmChart` resources (helm.cattle.io/v1), applied with `kubectl apply` by `make deploy` and reconciled by RKE2's helm-controller. Pin chart versions.
- **Grafana dashboard:** JSON in a ConfigMap labeled `grafana_dashboard: "1"`.
- **CI/CD:** GitHub Actions triggers on `paths: ["app/**"]` only (avoids loops with the bot's tag-bump commit). Least-privilege permissions (`contents: write`, `packages: write`), built-in `GITHUB_TOKEN`.
- **Secrets:** none in the repo. Grafana's admin password goes in a Kubernetes Secret created outside Git or a documented default for local use only, and the README says so.

## Documentation

- Keep `README.md` short and practical: prerequisites, `make up && make deploy`, URLs, architecture diagram, key decisions (summarized from `DECISIONS.md`), how to verify, production next steps.
- Write the README and code comments in **English**.
- If you make a decision that isn't covered in `DECISIONS.md` (e.g. a chart value or workaround), add a short entry to `DECISIONS.md` and tell me.

## Don'ts

- Don't install tools on the host beyond what the README lists (Multipass, kubectl, Docker/buildx, make; Go for local tests).
- Don't use `kubectl apply` for things that Argo CD manages once Argo CD is set up. Change Git instead.
- Don't expose the Kubernetes API publicly or add self-hosted runners.
- Don't over-engineer: no custom Helm chart for the app, no service mesh, no extra controllers beyond the plan.
