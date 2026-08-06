package phases

import (
	"context"
	"fmt"
	"os"

	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/protobuf/proto"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
)

// Phase15Jumphost provisions a testing jumphost VM and associated firewall
// rules in the external network to simulate external traffic sources.
// Feature-gated by testing.jumphost.enabled in cluster.yaml.
func Phase15Jumphost(ctx context.Context, cl *intent.Cluster, st *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 15] jumphost: cluster=%s\n", cl.Metadata.Name)

	if !cl.Testing.Jumphost.Enabled {
		fmt.Fprintln(os.Stderr, "[phase 15] skipped: jumphost disabled")
		return nil
	}

	jh := cl.Testing.Jumphost
	instanceType := jh.MachineType
	if instanceType == "" {
		instanceType = "e2-micro"
	}
	name := cl.Metadata.Name + "-jumphost"
	fwName := cl.Metadata.Name + "-allow-iap-ssh"

	if dryRun {
		fmt.Fprintf(os.Stderr, "[phase 15] dry-run: would create IAP firewall rule %s on %s\n", fwName, cl.Network.External.VPC)
		fmt.Fprintf(os.Stderr, "[phase 15] dry-run: would create jumphost instance %s (%s) in %s\n", name, instanceType, cl.Network.External.Subnet)
		st.Set("JUMPHOST_INSTANCE_NAME", name)
		st.Set("JUMPHOST_FIREWALL_NAME", fwName)
		return nil
	}

	// 1. Create IAP Firewall rule
	if err := ensureIAPFirewall(ctx, clients.Firewalls, cl.Metadata.Project, cl.Network.External.VPC, fwName); err != nil {
		return fmt.Errorf("phase15: firewall: %w", err)
	}
	st.Set("JUMPHOST_FIREWALL_NAME", fwName)

	// 2. Create Instance
	if err := ensureJumphostInstance(ctx, clients.Instances, cl.Metadata.Project, cl.Metadata.Zone, cl.Network.External.VPC, cl.Network.External.Subnet, name, instanceType); err != nil {
		return fmt.Errorf("phase15: instance: %w", err)
	}
	st.Set("JUMPHOST_INSTANCE_NAME", name)

	return st.Save()
}

func ensureIAPFirewall(ctx context.Context, firewalls gcp.FirewallsAPI, project, vpc, fwName string) error {
	_, err := firewalls.Get(ctx, &computepb.GetFirewallRequest{Project: project, Firewall: fwName})
	if err == nil {
		fmt.Fprintf(os.Stderr, "[phase 15] firewall %s already exists, skipping\n", fwName)
		return nil
	}
	if !isNotFound(err) {
		return fmt.Errorf("checking firewall %s: %w", fwName, err)
	}

	fmt.Fprintf(os.Stderr, "[phase 15] creating firewall %s (allow IAP SSH)\n", fwName)
	op, err := firewalls.Insert(ctx, &computepb.InsertFirewallRequest{
		Project: project,
		FirewallResource: &computepb.Firewall{
			Name:         proto.String(fwName),
			Network:      proto.String(networkSelfLink(project, vpc)),
			Direction:    proto.String("INGRESS"),
			SourceRanges: []string{"35.235.240.0/20"}, // IAP range
			Allowed: []*computepb.Allowed{
				{
					IPProtocol: proto.String("tcp"),
					Ports:      []string{"22"},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("creating firewall %s: %w", fwName, err)
	}
	return insertAndWait(ctx, op)
}

func ensureJumphostInstance(ctx context.Context, instances gcp.InstancesAPI, project, zone, vpc, subnet, name, machineType string) error {
	_, err := instances.Get(ctx, &computepb.GetInstanceRequest{Project: project, Zone: zone, Instance: name})
	if err == nil {
		fmt.Fprintf(os.Stderr, "[phase 15] instance %s already exists, skipping\n", name)
		return nil
	}
	if !isNotFound(err) {
		return fmt.Errorf("checking instance %s: %w", name, err)
	}

	fmt.Fprintf(os.Stderr, "[phase 15] creating jumphost instance %s\n", name)
	op, err := instances.Insert(ctx, &computepb.InsertInstanceRequest{
		Project: project,
		Zone:    zone,
		InstanceResource: &computepb.Instance{
			Name:        proto.String(name),
			MachineType: proto.String(fmt.Sprintf("zones/%s/machineTypes/%s", zone, machineType)),
			NetworkInterfaces: []*computepb.NetworkInterface{
				{
					Network:    proto.String(networkSelfLink(project, vpc)),
					Subnetwork: proto.String(fmt.Sprintf("projects/%s/regions/%s/subnetworks/%s", project, zoneToRegion(zone), subnet)),
				},
			},
			Disks: []*computepb.AttachedDisk{
				{
					Boot:       proto.Bool(true),
					AutoDelete: proto.Bool(true),
					InitializeParams: &computepb.AttachedDiskInitializeParams{
						SourceImage: proto.String("projects/debian-cloud/global/images/family/debian-12"),
						DiskSizeGb:  proto.Int64(20),
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("creating instance %s: %w", name, err)
	}
	return insertAndWait(ctx, op)
}

func zoneToRegion(zone string) string {
	// e.g., us-central1-a -> us-central1
	parts := len(zone) - 2
	if parts > 0 && zone[parts] == '-' {
		return zone[:parts]
	}
	return zone
}

// PhaseDownJumphost tears down the testing jumphost VM and its IAP firewall.
func PhaseDownJumphost(ctx context.Context, cl *intent.Cluster, st *state.State, clients *gcp.Clients) error {
	fmt.Fprintf(os.Stderr, "[phase 15 down] jumphost: cluster=%s\n", cl.Metadata.Name)

	name := st.Get("JUMPHOST_INSTANCE_NAME")
	if name == "" {
		name = cl.Metadata.Name + "-jumphost"
	}

	fwName := st.Get("JUMPHOST_FIREWALL_NAME")
	if fwName == "" {
		fwName = cl.Metadata.Name + "-allow-iap-ssh"
	}

	// Delete Instance
	fmt.Fprintf(os.Stderr, "[phase 15 down] deleting instance %s\n", name)
	op, err := clients.Instances.Delete(ctx, &computepb.DeleteInstanceRequest{
		Project:  cl.Metadata.Project,
		Zone:     cl.Metadata.Zone,
		Instance: name,
	})
	if err != nil {
		if !isNotFound(err) {
			return fmt.Errorf("deleting instance %s: %w", name, err)
		}
	} else {
		if err := insertAndWait(ctx, op); err != nil {
			return fmt.Errorf("waiting for instance %s deletion: %w", name, err)
		}
	}

	// Delete Firewall
	fmt.Fprintf(os.Stderr, "[phase 15 down] deleting firewall %s\n", fwName)
	op, err = clients.Firewalls.Delete(ctx, &computepb.DeleteFirewallRequest{
		Project:  cl.Metadata.Project,
		Firewall: fwName,
	})
	if err != nil {
		if !isNotFound(err) {
			return fmt.Errorf("deleting firewall %s: %w", fwName, err)
		}
	} else {
		if err := insertAndWait(ctx, op); err != nil {
			return fmt.Errorf("waiting for firewall %s deletion: %w", fwName, err)
		}
	}

	st.Delete("JUMPHOST_INSTANCE_NAME")
	st.Delete("JUMPHOST_FIREWALL_NAME")
	return st.Save()
}
