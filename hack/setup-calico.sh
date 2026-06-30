#!/usr/bin/env sh

set -eu

CALICO_VERSION="v3.32.0"
BASE_URL="https://raw.githubusercontent.com/projectcalico/calico/${CALICO_VERSION}/manifests"

# Taken from the official documentation https://docs.tigera.io/calico/latest/getting-started/kubernetes/kind#install-calico
#
# Create the Custom Resource Definitions (CRDs)
kubectl apply --server-side --force-conflicts -f "${BASE_URL}/operator-crds.yaml"
# Deploy the Tigera Operator that will reconcile the CRs
kubectl apply -f "${BASE_URL}/tigera-operator.yaml"
kubectl wait --for=condition=Available deployment/tigera-operator -n tigera-operator --timeout=300s

# This deploys the Goldmane deployment from which we will read flow logs
echo "\n-Deploy goldmane:\n"
kubectl apply -f "${BASE_URL}/custom-resources.yaml"

# Wait for the Goldmane certificates to be created
echo "\n-Wait for goldmane resources to be created:\n"
kubectl wait --for=create -n calico-system configmap/goldmane-ca-bundle --timeout=120s
kubectl wait --for=create -n calico-system secret/goldmane-key-pair --timeout=120s

# Create the secret for the CNI watcher
echo "\n-Creating CNI watcher secret:\n"
kubectl create secret generic cniwatcher-goldmane-key-pair \
  --from-file=ca.crt=<(kubectl -n calico-system get configmap goldmane-ca-bundle -o jsonpath='{.data.tigera-ca-bundle\.crt}') \
  --from-file=tls.crt=<(kubectl -n calico-system get secret goldmane-key-pair -o jsonpath='{.data.tls\.crt}' | base64 -d) \
  --from-file=tls.key=<(kubectl -n calico-system get secret goldmane-key-pair -o jsonpath='{.data.tls\.key}' | base64 -d) \
  -n calico-system \
  --dry-run=client -o yaml | kubectl apply -f -
