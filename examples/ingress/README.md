# Ingress Demo

Demonstrates BNK providing basic HTTP ingress into the GKE cluster using an `F5SPKIngress` resource. Traffic flows from an external testing jumphost to a pod running inside the cluster.

### 1. Provision Cluster
```bash
gkebnkctl up -f examples/ingress/cluster.yaml
export KUBECONFIG=.gkebnkctl/bnk-ingress/kubeconfig
```

### 2. Deploy the Workload and Ingress
This will deploy a simple NGINX deployment and a BNK `F5SPKIngress` to expose it on the external TMM interface.

```bash
kubectl apply -f examples/ingress/workload.yaml
kubectl apply -f examples/ingress/ingress.yaml
```

### 3. Verify Traffic from the Jumphost
To verify the ingress works, log into the jumphost and curl the VIP.
(Assuming the jumphost implementation logic is executed, you will run a script or SSH to the jumphost and `curl` the VIP exposed in `ingress.yaml`.)

### Teardown
```bash
kubectl delete -f examples/ingress/ingress.yaml
kubectl delete -f examples/ingress/workload.yaml
gkebnkctl down -f examples/ingress/cluster.yaml
```
