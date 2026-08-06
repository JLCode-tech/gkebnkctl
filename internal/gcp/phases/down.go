package phases

import (
	"context"
	"fmt"
	"os"

	computepb "cloud.google.com/go/compute/apiv1/computepb"
	containerpb "cloud.google.com/go/container/apiv1/containerpb"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
)

// PhaseDownCluster deletes the GKE cluster (node pools go with it) and waits
// until it is gone. Tolerates "already gone" (a 404 on GetCluster / a 404 on
// DeleteCluster).
func PhaseDownCluster(ctx context.Context, cl *intent.Cluster, _ *state.State, clients *gcp.Clients) error {
	project := cl.Metadata.Project
	zone := cl.Metadata.Zone
	name := cl.Metadata.Name

	fmt.Fprintf(os.Stderr, "[down] cluster: deleting %s (node pools go with it)\n", name)

	clusterName := fmt.Sprintf("projects/%s/locations/%s/clusters/%s", project, zone, name)
	if _, err := clients.GKE.GetCluster(ctx, &containerpb.GetClusterRequest{Name: clusterName}); err != nil {
		if isNotFound(err) {
			fmt.Fprintf(os.Stderr, "[down] cluster %s not found — skipping\n", name)
			return nil
		}
		return fmt.Errorf("down: cluster: GetCluster: %w", err)
	}

	op, err := clients.GKE.DeleteCluster(ctx, &containerpb.DeleteClusterRequest{Name: clusterName})
	if err != nil {
		if isNotFound(err) {
			fmt.Fprintf(os.Stderr, "[down] cluster %s already gone\n", name)
			return nil
		}
		return fmt.Errorf("down: cluster: DeleteCluster: %w", err)
	}
	if err := waitForGKEOp(ctx, clients.GKE, project, zone, op.GetName()); err != nil {
		return fmt.Errorf("down: cluster: waiting for delete: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[down] cluster %s deleted\n", name)
	return nil
}

// PhaseDownNetworks deletes ONLY the main VPC's firewall, subnet, and VPC
// (in that order — firewall/subnet before VPC). It deliberately never
// references network.external / network.internal — the shared dataplane
// VPCs may be used by other clusters and must never be touched by teardown,
// per docs/SPEC-go-tracer.md §3 "Down phases".
func PhaseDownNetworks(ctx context.Context, cl *intent.Cluster, _ *state.State, clients *gcp.Clients) error {
	project := cl.Metadata.Project
	region := cl.Region
	main := cl.Network.Main

	fwName := main.VPC + "-allow-internal"
	fmt.Fprintf(os.Stderr, "[down] main-vpc: deleting firewall %s\n", fwName)
	if _, err := clients.Firewalls.Get(ctx, &computepb.GetFirewallRequest{Project: project, Firewall: fwName}); err != nil {
		if isNotFound(err) {
			fmt.Fprintf(os.Stderr, "[down] firewall %s not found — skipping\n", fwName)
		} else {
			return fmt.Errorf("down: main-vpc: checking firewall %s: %w", fwName, err)
		}
	} else {
		op, err := clients.Firewalls.Delete(ctx, &computepb.DeleteFirewallRequest{Project: project, Firewall: fwName})
		if err != nil {
			if !isNotFound(err) {
				return fmt.Errorf("down: main-vpc: deleting firewall %s: %w", fwName, err)
			}
		} else if err := insertAndWait(ctx, op); err != nil {
			return fmt.Errorf("down: main-vpc: waiting for firewall delete: %w", err)
		}
	}

	fmt.Fprintf(os.Stderr, "[down] main-vpc: deleting subnet %s\n", main.Subnet)
	if _, err := clients.Subnetworks.Get(ctx, &computepb.GetSubnetworkRequest{Project: project, Region: region, Subnetwork: main.Subnet}); err != nil {
		if isNotFound(err) {
			fmt.Fprintf(os.Stderr, "[down] subnet %s not found — skipping\n", main.Subnet)
		} else {
			return fmt.Errorf("down: main-vpc: checking subnet %s: %w", main.Subnet, err)
		}
	} else {
		op, err := clients.Subnetworks.Delete(ctx, &computepb.DeleteSubnetworkRequest{Project: project, Region: region, Subnetwork: main.Subnet})
		if err != nil {
			if !isNotFound(err) {
				return fmt.Errorf("down: main-vpc: deleting subnet %s: %w", main.Subnet, err)
			}
		} else if err := insertAndWait(ctx, op); err != nil {
			return fmt.Errorf("down: main-vpc: waiting for subnet delete: %w", err)
		}
	}

	fmt.Fprintf(os.Stderr, "[down] main-vpc: deleting VPC %s\n", main.VPC)
	if _, err := clients.Networks.Get(ctx, &computepb.GetNetworkRequest{Project: project, Network: main.VPC}); err != nil {
		if isNotFound(err) {
			fmt.Fprintf(os.Stderr, "[down] VPC %s not found — skipping\n", main.VPC)
		} else {
			return fmt.Errorf("down: main-vpc: checking VPC %s: %w", main.VPC, err)
		}
	} else {
		op, err := clients.Networks.Delete(ctx, &computepb.DeleteNetworkRequest{Project: project, Network: main.VPC})
		if err != nil {
			if !isNotFound(err) {
				return fmt.Errorf("down: main-vpc: deleting VPC %s: %w", main.VPC, err)
			}
		} else if err := insertAndWait(ctx, op); err != nil {
			return fmt.Errorf("down: main-vpc: waiting for VPC delete: %w", err)
		}
	}

	fmt.Fprintf(os.Stderr, "[down] main-vpc done — shared dataplane VPCs %s/%s left untouched\n",
		cl.Network.External.VPC, cl.Network.Internal.VPC)
	return nil
}
