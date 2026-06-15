# Screenshot Demo Manifests

These manifests seed a live cluster with representative Kubernetes resources so
Astronomer's Cluster Explorer pages have data for screenshots.

They are intentionally isolated:

- Namespaced resources live in `astronomer-screenshot-demo`.
- Cross-namespace Gateway API reference examples live in
  `astronomer-screenshot-backend`.
- Cluster-scoped demo resources use the `astronomer-screenshot-demo` prefix.
- Every demo object carries `astronomer.io/demo=screenshots`.

## Install

Core Kubernetes objects:

```bash
kubectl apply -f deploy/samples/screenshot-demo-core.yaml
```

Optional Gateway API CRDs, including the experimental resources used by the UI
pages for TCPRoute, TLSRoute, and UDPRoute:

```bash
kubectl apply --server-side \
  -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.5.0/experimental-install.yaml
```

Gateway API screenshot objects:

```bash
kubectl apply -f deploy/samples/screenshot-demo-gateway-api.yaml
```

The Gateway API objects are for inventory and UI screenshots. They do not
install a Gateway controller and do not replace or mutate ingress-nginx.

## Cleanup

Remove demo objects:

```bash
kubectl delete -f deploy/samples/screenshot-demo-gateway-api.yaml --ignore-not-found
kubectl delete -f deploy/samples/screenshot-demo-core.yaml --ignore-not-found
```

The Gateway API CRDs are cluster-wide. Leave them installed if you want the
Gateway API pages to keep working. If this cluster is only a throwaway demo and
you want to remove the CRDs too:

```bash
kubectl delete --ignore-not-found \
  crd/backendtlspolicies.gateway.networking.k8s.io \
  crd/gatewayclasses.gateway.networking.k8s.io \
  crd/gateways.gateway.networking.k8s.io \
  crd/grpcroutes.gateway.networking.k8s.io \
  crd/httproutes.gateway.networking.k8s.io \
  crd/listenersets.gateway.networking.k8s.io \
  crd/referencegrants.gateway.networking.k8s.io \
  crd/tcproutes.gateway.networking.k8s.io \
  crd/tlsroutes.gateway.networking.k8s.io \
  crd/udproutes.gateway.networking.k8s.io \
  crd/xbackendtrafficpolicies.gateway.networking.x-k8s.io \
  crd/xmeshes.gateway.networking.x-k8s.io

kubectl delete --ignore-not-found \
  validatingadmissionpolicy/safe-upgrades.gateway.networking.k8s.io \
  validatingadmissionpolicybinding/safe-upgrades.gateway.networking.k8s.io
```
