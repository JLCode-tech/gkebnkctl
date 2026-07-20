# bnk-hostdev install order (qwiklabs BNY project, us-central1-a)

0. Node pools (after cluster Ready):
   - tmm-pool: 1x e2-standard-8, f5-dataplane=true,
     --additional-node-network network=tmm-ext,subnetwork=tmm-ext
     --additional-node-network network=tmm-int,subnetwork=tmm-int
     --additional-pod-network subnetwork=tmm-ext,pod-ipv4-range=tmm-ext-pods,max-pods-per-node=8
     --additional-pod-network subnetwork=tmm-int,pod-ipv4-range=tmm-int-pods,max-pods-per-node=8
   - agent-pool: 1x e2-standard-4, f5-dataplane=true,
     --additional-node-network network=tmm-ext,subnetwork=tmm-ext
     --additional-pod-network subnetwork=tmm-ext,pod-ipv4-range=tmm-ext-pods,max-pods-per-node=8
1. kubectl apply -f 01-namespaces.yaml
2. helm install cert-manager (jetstack repo, --set crds.enabled=true, ns cert-manager) + wait
3. kubectl apply -f 04-issuers.yaml (wait ext-ca Ready)
4. far-secret copy (authorized): for ns in f5-bnk f5-utils:
   kubectl --context REF get secret far-secret -n f5-bnk -o json | jq 'del(.metadata.uid,.metadata.resourceVersion,.metadata.creationTimestamp,.metadata.namespace,.metadata.annotations)' | kubectl --context NEW apply -n $ns -f -
5. kubectl apply -f 02-multus-ds.yaml (kube-system; nodeSelector f5-dataplane=true)
6. kubectl apply -f 03-nads-hostdevice.yaml
7. kubectl apply -f flo-manifest.yaml (from gkebnkctl-recon/; full rendered chart incl CRDs)
   + serviceaccount imagePullSecret if chart expects far-secret (values had global.imagePullSecrets)
8. kubectl apply -f 05-cneinstance.yaml; label tmm node app=f5-tmm
9. Discover GKE-allocated /28 slices (gcloud instances describe aliasIpRanges) -> render
   06-vlans-routes from tmpl -> apply after f5-spk-vlans CRD exists
10. Wait TMM 2/2 + F5SPKVlan Programmed; verify net1=real ens5 (host-device) inside TMM pod
11. Egress: F5SPKEgress pseudoCNI vxlan (port 5789 per allowed_services; nodeInterfaceName=ens5,
    tmmInterfaceName=external, ipv4Subnet 192.168.0.0/16 4-field set) in f5-bnk;
    test pod in egress-test ns (captured namespace list) on agent node
12. Success: clientside.pkts_in climbs on egress virtual; egress src = ext NAT IP (tmm-ext-nat)

Rollback/teardown: delete cluster bnk-hostdev + bnkhd-vpc/subnet/firewall (all ours; shared VPCs untouched).
