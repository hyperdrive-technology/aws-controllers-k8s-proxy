Kubebuilder Controller (universal-proxy-controller)

This controller watches ACK CRs and maintains the Envoy ConfigMap for the universal proxy. It computes a config from discovered resources and updates the ConfigMap. It also bumps a rollout annotation on the Envoy Deployment to reload the config.

Features
- Watches: RDS DBInstance, EC2 Instance, S3 Bucket (extendable)
- Builds HTTP/TCP listeners/clusters
- Writes `envoy.yaml` into a designated ConfigMap
- Patches Deployment template annotation `proxy.envoy/config-hash` to trigger a rollout

Dev quickstart
- Prereqs: Go 1.21+, kubebuilder, kustomize, docker
- Build:
```
make docker-build IMG=yourrepo/universal-proxy-controller:dev
```
- Deploy:
```
make deploy IMG=yourrepo/universal-proxy-controller:dev
```
- Configure via env vars:
  - `CONFIGMAP_NAMESPACE` (default: release namespace)
  - `CONFIGMAP_NAME` (default: release fullname)
  - `DEPLOYMENT_NAME` (Envoy Deployment name)
  - `DEPLOYMENT_NAMESPACE`

Extend
Add new reconcilers for additional ACK GVKs and extend the config builder.