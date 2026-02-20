# Demo: network-enforcer feedback loop

This sets up a minimal scenario where you can watch the network-enforcer
create and populate `NetworkPolicyProposal` resources from real traffic.

## Prerequisites

- A Kubernetes cluster (k3d, kind, etc.)
- Linux nodes with kernel 5.8+ (OBI uses eBPF)
- Helm 3

## 1. Install the network-enforcer

The helm chart deploys both the controller and
[OBI](https://opentelemetry.io/docs/zero-code/obi/) (OpenTelemetry eBPF
Instrumentation) as a DaemonSet. OBI captures network flows via eBPF and sends
`obi.network.flow.bytes` metrics to the controller's OTLP receiver.

```bash
helm dependency build ./charts/network-enforcer
helm install network-enforcer ./charts/network-enforcer \
  -n network-enforcer --create-namespace
```

To disable OBI (e.g. if you already have it deployed separately):

```bash
helm install network-enforcer ./charts/network-enforcer \
  -n network-enforcer --create-namespace \
  --set obi.enabled=false
```

## 2. Deploy demo workloads

```bash
kubectl apply -f hack/demo/workloads.yaml
```

This creates three deployments in the `demo` namespace:

- **frontend** → curls `backend:8080` every 5s
- **backend** → curls `postgres:5432` every 10s
- **postgres** → idle listener

## 3. Watch proposals

```bash
# Wait ~30-60s for OBI to pick up flows and the controller to reconcile
kubectl get networkpolicyproposals -n demo -w
```

Once proposals appear, inspect them:

```bash
kubectl get networkpolicyproposals -n demo -o yaml
```

You should see `deployment-frontend`, `deployment-backend`, and
`deployment-postgres` with ingress/egress rules matching the observed traffic.

## 4. (Optional) Enforce a proposal

Requires Calico CNI:

```bash
kubectl label networkpolicyproposal deployment-backend -n demo \
  security.rancher.io/enforce=true

kubectl get networkpolicies.crd.projectcalico.org -n demo
```

## Fallback: simulate flows without OBI

If your environment doesn't support eBPF (e.g. macOS with `make run`), you
can use the flow simulator to send fake OTLP metrics directly:

```bash
# Terminal 1: run the controller locally
make run

# Terminal 2: send fake flows to localhost:4317
go run ./hack/demo/simulate-flows

# Terminal 3: apply workloads and watch
kubectl apply -f hack/demo/workloads.yaml
kubectl get networkpolicyproposals -n demo -w
```
