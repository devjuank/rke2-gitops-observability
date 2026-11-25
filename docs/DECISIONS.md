# Technical Decisions

Short decision records: what was chosen, what was discarded, and why. The exercise asks for a **local, single-node RKE2** environment that can be built in **5–10 hours** and that **someone else can reproduce**. Those three constraints drive most of the choices below.

---

## 1. Where to run Linux: Multipass VM on the Mac

**Chosen:** Multipass (Canonical) running an Ubuntu 24.04 arm64 VM.

**Why**
- The exercise explicitly asks for a *local* environment. RKE2 needs a real Linux host with systemd.
- One command creates the VM. It uses Apple's native virtualization, so it is fast on Apple Silicon.
- It supports `--cloud-init` natively, so provisioning stays as code.
- Any reviewer can reproduce it with `brew install multipass` and `make up`.

**Discarded**
- **AWS EC2 / Azure VM:** closer to a real server, but it needs a cloud account, credentials, networking and cost management. That makes it harder for reviewers to reproduce, and it isn't "local".
- **kind / k3d / Docker Desktop Kubernetes:** easy, but they aren't RKE2. The exercise asks for RKE2 specifically.
- **Vagrant / UTM / VirtualBox:** they work, but they are heavier and VirtualBox has limited Apple Silicon support. Multipass is simpler for Ubuntu.

---

## 2. Creating the VM: Makefile, not Terraform

**Chosen:** a `Makefile` with `up`, `kubeconfig`, `deploy`, `status` and `down` targets wrapping `multipass` and `kubectl`.

**Why**
- A local, disposable VM gains nothing from Terraform's state management.
- The Multipass Terraform provider is community-maintained, with low adoption and uncertain Apple Silicon support. It's not worth the risk here.
- `make up` is the most universal entry point for reviewers.

**Discarded**
- **Terraform + Multipass provider:** extra tooling and risk for no real benefit on one local VM.

**In production:** Terraform would provision the VMs (e.g. Azure) and pass the **same cloud-init** file as `custom_data`.

---

## 3. Installing RKE2: cloud-init, not Ansible

**Chosen:** cloud-init writes `/etc/rancher/rke2/config.yaml`, runs the official installer and enables `rke2-server` at first boot.

**Why**
- It runs automatically on first boot, with no SSH, inventory or control machine. VM and cluster come up with one command.
- It's the standard bootstrap mechanism across clouds (EC2 `user_data`, Azure `custom_data`), so the same file is portable to their Azure servers.
- Rancher itself and Cluster API provision nodes with cloud-init.
- With one node and one install, Ansible's strengths (ordering across nodes, idempotent re-runs) aren't needed.

**Trade-offs**

| | cloud-init | Ansible |
|---|---|---|
| When it runs | Once, at first boot | Any time, on demand |
| Needs | Nothing (built into the image) | SSH, inventory, control node |
| Idempotent re-runs | No | Yes |
| Multi-node orchestration | No | Yes (server first, then agents with token) |
| Best for | Bootstrap, immutable/autoscaled nodes | Day-2 ops, long-lived on-prem servers |

**Discarded (for this exercise)**
- **Ansible** (e.g. `rancherfederal/rke2-ansible`, `lablabs.rke2`): the right tool for multi-node clusters and day-2 OS management, but overkill for a single disposable node.

**Upgrades:** cloud-init doesn't handle them. For RKE2, the native way is Rancher's **System Upgrade Controller** (declarative `Plan`, node by node with cordon/drain). The alternatives are immutable replacement (new node → join → drain old) or an Ansible rolling-upgrade playbook for on-prem fleets.

---

## 4. Single-node cluster

**Chosen:** one RKE2 server node acting as both control plane and worker.

**Why:** the exercise asks for it explicitly, and it fits the laptop's resources.

**In production:** three server nodes for etcd quorum and HA, plus dedicated agent nodes.

---

## 5. Container registry: GitHub Container Registry

**Chosen:** ghcr.io, public package, multi-arch image (arm64 + amd64).

**Why**
- It's free and lives next to the repo. GitHub Actions can push to it with the built-in token.
- A public image means no `imagePullSecret`, which makes the setup simpler to reproduce.
- A multi-arch image runs on the arm64 VM and on typical amd64 servers.

**Discarded**
- **`docker save` + `ctr images import` into RKE2's containerd:** fast but manual, not reproducible, and it breaks GitOps.
- **In-cluster registry:** more moving parts for no gain here.
- **Docker Hub:** works, but has rate limits and lives in a separate account.

---

## 6. App manifests: Kustomize, not a custom Helm chart

**Chosen:** plain YAML organized with Kustomize (`kubectl apply -k`).

**Why**
- It's built into kubectl, so there's nothing to install.
- Reviewers read plain, readable manifests instead of templates.
- One app and one environment don't need Helm's templating.
- Argo CD supports Kustomize natively.

**Discarded**
- **Own Helm chart:** more boilerplate. It pays off with several environments, services or external consumers.

---

## 7. Prometheus + Grafana: kube-prometheus-stack via RKE2's HelmChart

**Chosen:** the community `kube-prometheus-stack` chart (pinned to 93.10.1), declared as a `HelmChart` resource so RKE2's built-in **helm-controller** installs it.

**Why**
- One chart gives Prometheus, the Prometheus Operator, Grafana, Alertmanager, node-exporter, kube-state-metrics and default cluster dashboards. That covers infrastructure metrics out of the box.
- The Operator enables **ServiceMonitor**, a declarative, per-app way to configure scraping.
- The RKE2-native HelmChart mechanism keeps everything declarative, with no manual `helm install`.
- **Delivery of the HelmChart resources:** `make deploy` runs `kubectl apply` on the `HelmChart` objects from `deploy/monitoring` and `deploy/argocd`. RKE2's helm-controller reconciles them exactly as if they were dropped into `/var/lib/rancher/rke2/server/manifests/`, but there is one copy in the repo and no file transfer into the VM.

**Discarded**
- **`helm install` from the Mac:** works, but it's an imperative step outside the repo's declarative flow.
- **Raw Prometheus/Grafana manifests or separate charts:** more work, and no Operator or ServiceMonitor.

**Chart values worth noting**
- **Alertmanager disabled:** alerting is out of scope (SPEC §4). It saves memory and a component nobody would use. SLO-based alerting is in the production next steps.
- **etcd, scheduler, controller-manager and kube-proxy monitors disabled:** on RKE2 they bind their metrics endpoints to `127.0.0.1`, so Prometheus would always show them as DOWN. Enabling them means changing RKE2's bind addresses, which adds nothing to what the exercise asks for.
- **`serviceMonitorSelectorNilUsesHelmValues: false`:** by default the chart only picks up ServiceMonitors labeled with its own release name. Turning it off lets any namespace's ServiceMonitor be discovered, so the app doesn't need to know about the monitoring release.
- **No persistence, 1-day retention:** persistent storage is out of scope; data is lost if the Prometheus pod restarts.
- **Grafana admin password:** `make deploy` creates a Kubernetes Secret with a random password the first time, and the chart reads it via `admin.existingSecret`. Nothing secret is stored in Git, and there is no well-known default password.
- **Prometheus Ingress:** exposed at `prometheus.<VM-IP>.nip.io` so the Targets page (AC-7) and ad-hoc queries are reachable without `port-forward`.

---

## 8. Grafana dashboard as code

**Chosen:** dashboard JSON in a ConfigMap labeled `grafana_dashboard: "1"`. The Grafana sidecar loads it automatically.

**Why:** it's versioned in Git, reproducible, and survives Grafana restarts without manual clicks.

**Panels (RED + resources):** request rate, error rate, p95/p99 latency, pod CPU and memory vs requests/limits, container restarts.

---

## 9. Delivery: Argo CD (GitOps)

**Chosen:** Argo CD installed via an RKE2 HelmChart, with an `Application` pointing to `deploy/app` and `automated: { prune: true, selfHeal: true }`.

**Why**
- Git is the source of truth: a commit changes the cluster, and manual drift is reverted automatically.
- No cluster credentials are needed in CI. The cluster pulls from GitHub. This matters here because GitHub Actions can't reach a VM on a laptop.
- It aligns with the team's pain point around **deployments**.

**Discarded**
- **`kubectl apply` from a Makefile only:** simplest. It stays as a fallback (`make deploy`), but it's push-based and drift isn't corrected.
- **Push-based deploy from GitHub Actions:** it needs the runner to reach the cluster (self-hosted runner or exposed API). That isn't possible or desirable with a local VM.
- **Flux:** an equally valid GitOps tool. Argo CD was chosen for its UI, which helps in the walkthrough, and for prior hands-on experience.

**Trade-off:** Argo CD adds memory usage, so the VM is sized to 8 GB.

### 9b. Continuous deployment: CI commits the new image tag, Argo CD deploys

**Chosen:** on every push that touches `app/`, GitHub Actions runs the tests, builds and pushes a multi-arch image tagged with the commit SHA, then runs `kustomize edit set image` and commits the new tag to `deploy/app`. Argo CD sees the Git change and rolls it out.

**Why**
- End-to-end CD without GitHub ever reaching the cluster. The VM can stay private on the laptop, and the same model works for clusters behind firewalls (like on-prem RKE2).
- Every deployment is a Git commit: auditable, reviewable, and rolled back with `git revert`.
- Immutable SHA tags instead of `latest`, so each running pod is traceable to an exact build.
- It's the most common GitOps pattern. No extra components to run.

**Discarded**
- **Self-hosted GitHub runner inside the VM** running `kubectl apply`: it works, but it gives CI cluster credentials, it's push-based, and it's an extra component to keep alive.
- **Argo CD Image Updater:** watches the registry and updates tags automatically. It's elegant, but it's another controller to install and configure (registry access, write-back credentials). It's a reasonable next step at scale.
- **Exposing the Kubernetes API to GitHub:** not acceptable from a security point of view.

**Details**
- The workflow filters on `paths: ["app/**"]`, so the bot's commit to `deploy/` doesn't trigger another build (no loop).
- Permissions are least-privilege: `contents: write` and `packages: write` only, using the built-in `GITHUB_TOKEN`.
- At scale I'd split the app repo and the config repo, and promote between environments with pull requests.

### 9c. Deployment strategy: rolling update (canary as next step)

**Chosen:** the native Kubernetes `RollingUpdate` with `maxSurge: 1` and `maxUnavailable: 0`. Kubernetes starts one new pod, waits until its readiness probe passes, and only then removes an old one.

**Why**
- Zero downtime with no extra components: readiness gates the traffic, and rollback is a `git revert`.
- It shows how probes and deployments work together, which is the core of the exercise.
- It's enough for a single small service with 2 replicas.

**Discarded (for now)**
- **Canary / blue-green with Argo Rollouts:** not native to Kubernetes. They need the Argo Rollouts controller and a `Rollout` resource instead of a `Deployment`, plus traffic splitting through the ingress. That's valuable, but it's more work than this exercise needs.
- **Recreate:** causes downtime. Only useful when two versions can't run at the same time.

**Next step:** Argo Rollouts (it integrates with Argo CD) with a **canary + Prometheus analysis**. Send ~20% of traffic to the new version, query the error rate and p95 latency from Prometheus, promote automatically if they stay within the SLO, and **roll back automatically** if they don't. This connects the deployment pain point with the observability work. Use blue-green for changes that need an instant cut-over and a fast switch back.

---

## 10. Access from the Mac: Ingress + nip.io

**Chosen:** RKE2's bundled **Traefik** ingress controller (`ingressClassName: traefik`). The app Ingress is **hostless**; Grafana and Argo CD use `grafana.<VM-IP>.nip.io` / `argocd.<VM-IP>.nip.io`.

**Why**
- It's the realistic, production-like path (L7 routing on 80/443) using what RKE2 already ships.
- Upstream ingress-nginx was retired in March 2026. Since RKE2 v1.36, Traefik is the default for new clusters, and the ingress-nginx chart is removed in v1.37. Pinning RKE2 v1.36 with ingress-nginx would mean building on a retired component.
- **Hostless app Ingress:** the VM IP comes from DHCP and differs per machine and per rebuild. `deploy/app` is synced from Git by Argo CD, so it can't contain the IP. A rule without `host` matches any hostname, so both `http://<VM-IP>/` and `http://app.<VM-IP>.nip.io/` work.
- Grafana/Argo CD hostnames live in HelmChart values (not managed by Argo CD), so `make deploy` fills in the VM IP when applying them.
- nip.io resolves to the embedded IP, so there are no `/etc/hosts` edits.

**Discarded**
- **NodePort:** works, but uses random high ports and is less realistic.
- **`kubectl port-forward`:** fine for debugging, but not "accessible" in a durable sense.
- **LoadBalancer + MetalLB:** unnecessary on a single node with an Ingress controller already present.
- **ingress-nginx on RKE2 v1.36** (`ingress-controller: ingress-nginx`) or **RKE2 v1.35** to keep it as default: both rely on a retired upstream project.
- **Fixed hostnames + `/etc/hosts`** (e.g. `*.rke2.test` with a `make hosts` target): cleaner URLs, but needs `sudo` on every machine and again whenever the VM IP changes.
- **`*.localhost` + port-forward:** no DNS needed, but depends on a long-running `kubectl port-forward`.
- **Local DNS server (dnsmasq):** an extra host tool and root service for every reviewer, and it still has to be edited when the VM IP changes.

**Known issue: routers with DNS rebinding protection.** Some routers drop public DNS answers that point to private IPs, so `*.nip.io` doesn't resolve. The fix is a per-domain resolver on macOS, `/etc/resolver/nip.io` with `nameserver 1.1.1.1`: only `*.nip.io` lookups bypass the router, everything else (local names, VPN domains) is unchanged, and it keeps working when the VM IP changes. Changing the Mac's DNS globally was discarded for that reason. `make deploy` detects the problem and prints the fix; the app also answers on `http://<VM-IP>/` in the meantime.

---

## 11. Health checks and resources

**Chosen**
- **Liveness `/healthz`:** returns 200 whenever the HTTP server responds, with no dependency checks. If it fails, the container is restarted (deadlock or hang).
- **Readiness `/readyz`:** returns 200 only after initialization completes, and 503 during startup and on SIGTERM. If it fails, the pod is removed from the Service endpoints without a restart.
- **Requests/limits:** small CPU/memory requests for scheduling and a memory limit to contain leaks. The CPU limit is kept generous to avoid throttling.
- **Graceful shutdown:** on SIGTERM, mark not-ready, wait briefly for endpoints to update, then drain in-flight requests.

**Why:** liveness must not check dependencies. A database outage would otherwise restart every pod and make things worse. Readiness handles temporary unavailability.

---

## 12. The Go application: own small service with a simulated workload

**Chosen:** a small purpose-built Go service with `/`, `/work`, `/healthz`, `/readyz` and `/metrics`. `/work` adds random latency and a configurable error rate. Behavior is controlled by env vars (`WORK_MAX_LATENCY_MS`, `WORK_ERROR_RATE`, `STARTUP_DELAY_SECONDS`).

**Why**
- Full control over the metrics (RED: rate, errors, duration) and over the probe semantics. Both are easy to explain in the walkthrough.
- `/work` makes the dashboard meaningful. A healthy hello-world shows flat lines; a realistic workload shows latency percentiles and an error rate.
- `STARTUP_DELAY_SECONDS` makes the readiness probe observable: the pod is Running but not Ready until init finishes.
- Env-var configuration lets the demo change behavior through Git and Argo CD, with no rebuild.

**Discarded**
- **An existing open-source Go app** (e.g. podinfo): solid, but it brings features and metrics I didn't design, so there's less to show about my own decisions. podinfo stays a good reference for patterns.

---

## 13. Architecture: arm64 locally, multi-arch images

**Context:** the Mac is Apple Silicon (M2 Pro, 16 GB), so the VM is arm64. RKE2, kube-prometheus-stack and Argo CD all publish arm64 images.

**Chosen:** build the app image for `linux/arm64` and `linux/amd64` with `docker buildx`, so it runs locally and on typical amd64 servers.
