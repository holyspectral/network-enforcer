# Setup

## Kind + tilt

```bash
kind create cluster --config=./hack/kind-no-cni.yaml
# the CNI used by tilt is the one you set in the `tilt-settings.yaml`
tilt up
```

Deploy a test workload

```bash
kubectl apply -f ./hack/demo/workloads.yaml
```

See the Network Policies Proposals generated

```bash
kubectl get npp -n demo
```
