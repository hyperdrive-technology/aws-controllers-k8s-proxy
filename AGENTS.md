# AGENTS.md

## Cursor Cloud specific instructions

This repo has two deliverables:

- `charts/universal-proxy/` — Helm chart that deploys a single Envoy "universal proxy" Deployment plus one Service per backend (the primary product). See `README.md`.
- `kubebuilder/` — Go controller (`example.com/universal-proxy-controller`) that watches ACK CRs (S3 `Bucket`, DynamoDB `Table`, RDS `DBInstance`, EC2 `Instance`) and writes the Envoy `envoy.yaml` into a ConfigMap, then bumps a rollout annotation on the Envoy Deployment. See `kubebuilder/README.md`.

Note: most code lives on feature branches; the `main` branch may contain only scaffolding (`README.md`, `LICENSE`, `.gitignore`). Commands below assume the `kubebuilder/` and `charts/` directories are present.

### Go controller (`kubebuilder/`)
- Build / vet / test: `cd kubebuilder && go build ./...`, `go vet ./...`, `go test ./...` (no tests currently). Go 1.21+ (snapshot has 1.22).
- Gotcha: `go build ./...` writes a binary named `universal-proxy-controller` into `kubebuilder/`, overwriting the file of the same name that is committed in the repo. After building, run `git checkout -- universal-proxy-controller` to avoid committing a changed binary.
- The controller needs a Kubernetes API server. It uses `ctrl.GetConfigOrDie()`, so set `KUBECONFIG`. Behavior is driven by env vars `CONFIGMAP_NAMESPACE`/`CONFIGMAP_NAME`/`DEPLOYMENT_NAMESPACE`/`DEPLOYMENT_NAME` (see `loadConfig`); it reconciles every 15s.

### Running against a cluster (important caveat)
- `kind`/`k3d`/`minikube` (container-based clusters) do NOT work in the Cursor Cloud VM: the delegated cgroup v2 hierarchy lacks the `memory` controller and cannot be restructured (`failed to find memory cgroup (v2)` / writing `cgroup.subtree_control` or `cgroup.procs` returns `Operation not supported`). Docker itself runs (`sudo dockerd`, fuse-overlayfs storage driver) but cannot host a kubelet.
- Use `envtest` instead (controller-runtime), which runs `kube-apiserver` + `etcd` as plain processes — no containers/cgroups. Fetch binaries with a recent `setup-envtest` (`go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest`; the old GCS index 401s, so `@latest`'s new release index is required), e.g. `setup-envtest use 1.29.0 -p path`, then point `KUBEBUILDER_ASSETS` at the returned dir. envtest has no kubelet/scheduler, so `Deployment` pods will not start (fine for testing the controller's ConfigMap reconcile; CRDs/CRs can be applied with `kubectl`).

### Helm chart (`charts/universal-proxy/`)
- Lint: `helm lint charts/universal-proxy`. Render: `helm template up charts/universal-proxy -f <values.yaml>`. Add backends under `http.services` / `tcp.services` in values (examples in `charts/universal-proxy/values.yaml` and `README.md`); each entry yields a listener/cluster and a dedicated K8s Service.

### Tooling
- `go`, `helm`, `kubectl`, `docker`, `setup-envtest` are provisioned in the VM snapshot (not in the update script, which only refreshes Go modules). `sudo dockerd` must be started manually if Docker is needed.
