Universal Proxy (Envoy) for ACK-managed Services

This repo includes a Helm chart that deploys a single Envoy-based "universal proxy" Deployment. It fronts multiple backends (AWS services or EC2/RDS endpoints) and exposes a separate Kubernetes Service per backend. All Services point to the same Envoy pods, each with its own listener/route.

Highlights
- One Envoy Deployment for N backends
- One K8s Service per backend (good for Telepresence intercepts)
- HTTP per-service listeners with optional AWS SigV4 signing (S3/DynamoDB/Lambda/API GW)
- TCP per-service listeners for DBs/brokers (RDS, DocDB, Redis, MQ, MSK)
- Optional IRSA on the ServiceAccount for signing use cases

Getting started
1) Add/modify services in `charts/universal-proxy/values.yaml` under `http.services` and `tcp.services`.
2) Install the chart (example):

```
helm upgrade -i up charts/universal-proxy \
  --namespace dev --create-namespace \
  -f charts/universal-proxy/values.yaml
```

Example values
Add S3 (signed), DynamoDB (signed), and Postgres (TCP) in `values.yaml`:

```
http:
  services:
  - name: s3-photos
    listenerPort: 18080
    servicePort: 80
    upstream:
      host: s3.ap-southeast-2.amazonaws.com
      port: 443
      tls: true
    routing:
      prefixRewrite: "/photos"
    sigv4:
      enabled: true
      serviceName: s3
      region: ap-southeast-2
      useUnsignedPayload: true
  - name: dynamodb-orders
    listenerPort: 18081
    servicePort: 80
    upstream:
      host: dynamodb.ap-southeast-2.amazonaws.com
      port: 443
      tls: true
    sigv4:
      enabled: true
      serviceName: dynamodb
      region: ap-southeast-2
      useUnsignedPayload: false

tcp:
  services:
  - name: rds-app
    listenerPort: 15432
    servicePort: 5432
    upstream:
      host: mydb.abcxyz.ap-southeast-2.rds.amazonaws.com
      port: 5432
      tls: true
```

IRSA (optional)
If you need Envoy to sign AWS API requests (S3/DDB/Lambda/etc.), attach an IAM role to the ServiceAccount:

```
serviceAccount:
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::123456789012:role/universal-proxy-role
```

Telepresence: personal intercepts
Because each backend has its own K8s Service, developers can intercept a single target without affecting others:

- S3 (use a local emulator like MinIO/LocalStack on port 4566):
```
telepresence connect
telepresence intercept s3-photos --port 4566:80
```
- DynamoDB (DynamoDB Local on 8000):
```
telepresence intercept dynamodb-orders --port 8000:80
```
- Postgres (local postgres on 5432):
```
telepresence intercept rds-app --port 5432:5432
```

Notes
- Each HTTP service is a dedicated listener, so SigV4 can be enabled per-service independently.
- Each TCP service is a dedicated listener for reliability (TLS per upstream as needed).
- All Services select the same Deployment (`app=<chart-name>`), so ops is simple and Telepresence remains per-Service.

Next steps (optional)
- Automate values generation via a small controller that watches ACK CRs and emits/updates the chart values or serves Envoy xDS dynamically.
- Add health endpoints or admin access as desired (Envoy admin bound to 127.0.0.1:9901 by default).