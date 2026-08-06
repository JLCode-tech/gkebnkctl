# Multi-Protocol Ingress Demo

Demonstrates BNK providing advanced multi-protocol ingress (e.g., exposing both a web interface via HTTP and a streaming/management interface via TCP/UDP on the same VIP) into the GKE cluster.

### 1. Provision Cluster
```bash
gkebnkctl up -f examples/multi-protocol-ingress/cluster.yaml
export KUBECONFIG=.gkebnkctl/bnk-multiprotocol/kubeconfig
```

### 2. Deploy the Workloads and Ingress
This will deploy multiple backend services and use a BNK `F5SPKIngress` to expose them across different ports and protocols on a single VIP.

```bash
kubectl apply -f examples/multi-protocol-ingress/workload.yaml
kubectl apply -f examples/multi-protocol-ingress/ingress.yaml
```

### 3. Verify Traffic from the Jumphost
To verify the multi-protocol capabilities, log into the jumphost and test connectivity across different ports and protocols against the VIP.

### Teardown
```bash
kubectl delete -f examples/multi-protocol-ingress/ingress.yaml
kubectl delete -f examples/multi-protocol-ingress/workload.yaml
gkebnkctl down -f examples/multi-protocol-ingress/cluster.yaml
```
