package phases

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	compute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"github.com/googleapis/gax-go/v2/apierror"
	"google.golang.org/api/googleapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
)

// mainVPCDefaultMTU matches the bash seed's net::create_vpc default (the
// main VPC has no operator-set mtu field in cluster.yaml — see
// intent.MainNetworkSpec).
const mainVPCDefaultMTU = 1460

// secondaryRange pairs a GKE secondary-range name with its CIDR. A slice
// (not a map) so subnet creation is deterministic and testable.
type secondaryRange struct {
	name string
	cidr string
}

// Phase10Networks creates the cluster-OWNED main VPC (subnet + 2 secondary
// ranges + allow-internal firewall) and, if missing, the SHARED external/
// internal dataplane VPCs (subnet + 1 secondary range + allow-internal
// firewall each) + Cloud Router/NAT. Every resource is named deterministically
// from cluster.yaml — idempotency is Get(name) -> skip-if-exists, per
// docs/SPEC-go-tracer.md §4.
//
// The external/internal VPCs are NEVER deleted by PhaseDownNetworks (see
// down.go) — they may be shared by other clusters.
func Phase10Networks(ctx context.Context, cl *intent.Cluster, _ *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 10] networks: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		printPhase10Plan(cl)
		return nil
	}

	main := cl.Network.Main
	if err := ensureVPC(ctx, clients.Networks, cl.Metadata.Project, main.VPC, mainVPCDefaultMTU); err != nil {
		return fmt.Errorf("phase10: main vpc: %w", err)
	}
	if err := ensureSubnet(ctx, clients.Subnetworks, cl.Metadata.Project, cl.Region, main.VPC, main.Subnet, main.CIDR, []secondaryRange{
		{name: main.PodsRangeName, cidr: main.PodsCIDR},
		{name: main.SvcsRangeName, cidr: main.SvcsCIDR},
	}); err != nil {
		return fmt.Errorf("phase10: main subnet: %w", err)
	}
	if err := ensureAllowInternalFW(ctx, clients.Firewalls, cl.Metadata.Project, main.VPC, cl.Network.AllowInternalCidr); err != nil {
		return fmt.Errorf("phase10: main firewall: %w", err)
	}

	for _, dp := range []struct {
		label string
		spec  intent.DataplaneSpec
	}{
		{"external", cl.Network.External},
		{"internal", cl.Network.Internal},
	} {
		if err := ensureVPC(ctx, clients.Networks, cl.Metadata.Project, dp.spec.VPC, int32(dp.spec.MTU)); err != nil { //nolint:gosec,g115 -- MTU is validated as a positive int at intent.Load
			return fmt.Errorf("phase10: %s vpc: %w", dp.label, err)
		}
		if err := ensureSubnet(ctx, clients.Subnetworks, cl.Metadata.Project, cl.Region, dp.spec.VPC, dp.spec.Subnet, dp.spec.CIDR, []secondaryRange{
			{name: dp.spec.PodsRangeName, cidr: dp.spec.PodsCIDR},
		}); err != nil {
			return fmt.Errorf("phase10: %s subnet: %w", dp.label, err)
		}
		if err := ensureAllowInternalFW(ctx, clients.Firewalls, cl.Metadata.Project, dp.spec.VPC, cl.Network.AllowInternalCidr); err != nil {
			return fmt.Errorf("phase10: %s firewall: %w", dp.label, err)
		}
	}

	if cl.Network.NatGatewaysEnabled() {
		if err := ensureRouterNAT(ctx, clients.Routers, cl.Metadata.Project, cl.Region, cl.Network.External.VPC); err != nil {
			return fmt.Errorf("phase10: external router+nat: %w", err)
		}
		if err := ensureRouterNAT(ctx, clients.Routers, cl.Metadata.Project, cl.Region, cl.Network.Internal.VPC); err != nil {
			return fmt.Errorf("phase10: internal router+nat: %w", err)
		}
	} else {
		fmt.Fprintln(os.Stderr, "[phase 10] network.natGateways=false — skipping Cloud NAT")
	}

	fmt.Fprintln(os.Stderr, "[phase 10] networks OK")
	return nil
}

func printPhase10Plan(cl *intent.Cluster) {
	main := cl.Network.Main
	fmt.Fprintf(os.Stderr, "[phase 10] would create VPC %s (%s) + subnet %s [pods %s=%s, svcs %s=%s] + fw %s-allow-internal\n",
		main.VPC, main.CIDR, main.Subnet, main.PodsRangeName, main.PodsCIDR, main.SvcsRangeName, main.SvcsCIDR, main.VPC)
	for _, dp := range []struct {
		label string
		spec  intent.DataplaneSpec
	}{
		{"external", cl.Network.External},
		{"internal", cl.Network.Internal},
	} {
		fmt.Fprintf(os.Stderr, "[phase 10] would create SHARED VPC %s (mtu %d) + subnet %s [pods %s=%s] + fw %s-allow-internal (skip if exists)\n",
			dp.spec.VPC, dp.spec.MTU, dp.spec.Subnet, dp.spec.PodsRangeName, dp.spec.PodsCIDR, dp.spec.VPC)
		if cl.Network.NatGatewaysEnabled() {
			fmt.Fprintf(os.Stderr, "[phase 10] would create Cloud Router+NAT %s-router / %s-nat\n", dp.spec.VPC, dp.spec.VPC)
		}
	}
}

// networkSelfLink returns the partial resource-name form GCP compute APIs
// accept for a Network reference field ("projects/P/global/networks/N").
func networkSelfLink(project, vpc string) string {
	return fmt.Sprintf("projects/%s/global/networks/%s", project, vpc)
}

// insertAndWait blocks until op completes. A nil op (as fakes return to
// signal an already-done synchronous mutation — see docs/SPEC-go-tracer.md
// §7, *compute.Operation has no exported constructor so fakes cannot build a
// real "done" instance) is treated as already-done.
func insertAndWait(ctx context.Context, op *compute.Operation) error {
	if op == nil {
		return nil
	}
	return op.Wait(ctx)
}

func ensureVPC(ctx context.Context, networks gcp.NetworksAPI, project, vpc string, mtu int32) error {
	_, err := networks.Get(ctx, &computepb.GetNetworkRequest{Project: project, Network: vpc})
	if err == nil {
		fmt.Fprintf(os.Stderr, "[phase 10] VPC %s already exists, skipping\n", vpc)
		return nil
	}
	if !isNotFound(err) {
		return fmt.Errorf("checking VPC %s: %w", vpc, err)
	}

	fmt.Fprintf(os.Stderr, "[phase 10] creating VPC %s (mtu=%d)\n", vpc, mtu)
	op, err := networks.Insert(ctx, &computepb.InsertNetworkRequest{
		Project: project,
		NetworkResource: &computepb.Network{
			Name:                  proto.String(vpc),
			AutoCreateSubnetworks: proto.Bool(false),
			Mtu:                   proto.Int32(mtu),
		},
	})
	if err != nil {
		return fmt.Errorf("creating VPC %s: %w", vpc, err)
	}
	return insertAndWait(ctx, op)
}

func ensureSubnet(ctx context.Context, subnets gcp.SubnetworksAPI, project, region, vpc, subnet, cidr string, ranges []secondaryRange) error {
	_, err := subnets.Get(ctx, &computepb.GetSubnetworkRequest{Project: project, Region: region, Subnetwork: subnet})
	if err == nil {
		fmt.Fprintf(os.Stderr, "[phase 10] subnet %s already exists, skipping\n", subnet)
		return nil
	}
	if !isNotFound(err) {
		return fmt.Errorf("checking subnet %s: %w", subnet, err)
	}

	secondary := make([]*computepb.SubnetworkSecondaryRange, 0, len(ranges))
	for _, r := range ranges {
		if r.name == "" {
			continue
		}
		secondary = append(secondary, &computepb.SubnetworkSecondaryRange{
			RangeName:   proto.String(r.name),
			IpCidrRange: proto.String(r.cidr),
		})
	}

	fmt.Fprintf(os.Stderr, "[phase 10] creating subnet %s (%s) in %s\n", subnet, cidr, vpc)
	op, err := subnets.Insert(ctx, &computepb.InsertSubnetworkRequest{
		Project: project,
		Region:  region,
		SubnetworkResource: &computepb.Subnetwork{
			Name:              proto.String(subnet),
			Network:           proto.String(networkSelfLink(project, vpc)),
			IpCidrRange:       proto.String(cidr),
			SecondaryIpRanges: secondary,
		},
	})
	if err != nil {
		return fmt.Errorf("creating subnet %s: %w", subnet, err)
	}
	return insertAndWait(ctx, op)
}

func ensureAllowInternalFW(ctx context.Context, firewalls gcp.FirewallsAPI, project, vpc, allowCIDR string) error {
	name := vpc + "-allow-internal"
	_, err := firewalls.Get(ctx, &computepb.GetFirewallRequest{Project: project, Firewall: name})
	if err == nil {
		fmt.Fprintf(os.Stderr, "[phase 10] firewall %s already exists, skipping\n", name)
		return nil
	}
	if !isNotFound(err) {
		return fmt.Errorf("checking firewall %s: %w", name, err)
	}

	fmt.Fprintf(os.Stderr, "[phase 10] creating firewall %s (allow all from %s)\n", name, allowCIDR)
	op, err := firewalls.Insert(ctx, &computepb.InsertFirewallRequest{
		Project: project,
		FirewallResource: &computepb.Firewall{
			Name:         proto.String(name),
			Network:      proto.String(networkSelfLink(project, vpc)),
			Direction:    proto.String("INGRESS"),
			SourceRanges: []string{allowCIDR},
			Allowed:      []*computepb.Allowed{{IPProtocol: proto.String("all")}},
		},
	})
	if err != nil {
		return fmt.Errorf("creating firewall %s: %w", name, err)
	}
	return insertAndWait(ctx, op)
}

// ensureRouterNAT creates a Cloud Router named "<vpc>-router" on vpc (if
// absent), then attaches a Cloud NAT named "<vpc>-nat" to it (if absent).
// These are two independent, separately-tolerant steps — matching bash
// net::create_nat (reference/bash-seed/bin/10-networks.sh:102-142) exactly:
//
//   - Router create: a 403 (compute.routers.create denied) is tolerated —
//     warn with a copy-paste owner command and return, WITHOUT attempting
//     the NAT attach (the router doesn't exist, so there's nothing to attach
//     it to). Any other router-create error is fatal.
//   - NAT attach (Patch): ANY error is non-fatal — warn and continue. This
//     applies whether the router was just freshly created or already
//     existed, matching bash's `|| { log::warn ...; }` on the NAT create
//     call.
func ensureRouterNAT(ctx context.Context, routers gcp.RoutersAPI, project, region, vpc string) error {
	routerName := vpc + "-router"
	natName := vpc + "-nat"

	existing, err := routers.Get(ctx, &computepb.GetRouterRequest{Project: project, Region: region, Router: routerName})
	switch {
	case err == nil:
		fmt.Fprintf(os.Stderr, "[phase 10] router %s already exists, skipping create\n", routerName)

	case isNotFound(err):
		fmt.Fprintf(os.Stderr, "[phase 10] creating Cloud Router %s on %s\n", routerName, vpc)
		op, insErr := routers.Insert(ctx, &computepb.InsertRouterRequest{
			Project: project,
			Region:  region,
			RouterResource: &computepb.Router{
				Name:    proto.String(routerName),
				Network: proto.String(networkSelfLink(project, vpc)),
			},
		})
		if insErr != nil {
			if isPermissionDenied(insErr) {
				fmt.Fprintf(os.Stderr, "[phase 10] warn: no permission to create router %s (compute.routers.create denied).\n", routerName)
				fmt.Fprintln(os.Stderr, "[phase 10] warn: ASK YOUR PROJECT OWNER TO RUN:")
				fmt.Fprintf(os.Stderr, "[phase 10] warn:   gcloud compute routers create %s --project=%s --network=%s --region=%s\n",
					routerName, project, vpc, region)
				fmt.Fprintf(os.Stderr, "[phase 10] warn:   gcloud compute routers nats create %s --router=%s --region=%s --auto-allocate-nat-external-ips --nat-all-subnet-ip-ranges --project=%s\n",
					natName, routerName, region, project)
				fmt.Fprintln(os.Stderr, "[phase 10] warn: egress will only reach in-VPC targets until this exists.")
				return nil
			}
			return fmt.Errorf("creating router %s on %s: %w", routerName, vpc, insErr)
		}
		if waitErr := insertAndWait(ctx, op); waitErr != nil {
			return fmt.Errorf("creating router %s on %s: %w", routerName, vpc, waitErr)
		}
		existing = nil // freshly created router has no NATs yet

	default:
		return fmt.Errorf("checking router %s: %w", routerName, err)
	}

	for _, n := range existing.GetNats() {
		if n.GetName() == natName {
			fmt.Fprintf(os.Stderr, "[phase 10] NAT %s already exists, skipping\n", natName)
			return nil
		}
	}

	fmt.Fprintf(os.Stderr, "[phase 10] creating Cloud NAT %s on router %s\n", natName, routerName)
	nats := append(existing.GetNats(), &computepb.RouterNat{
		Name:                          proto.String(natName),
		NatIpAllocateOption:           proto.String("AUTO_ONLY"),
		SourceSubnetworkIpRangesToNat: proto.String("ALL_SUBNETWORKS_ALL_IP_RANGES"),
	})
	op, patchErr := routers.Patch(ctx, &computepb.PatchRouterRequest{
		Project:        project,
		Region:         region,
		Router:         routerName,
		RouterResource: &computepb.Router{Nats: nats},
	})
	if patchErr != nil {
		fmt.Fprintf(os.Stderr, "[phase 10] warn: NAT create failed on %s (%v) — treating as non-fatal, matching router-create 403 handling.\n", routerName, patchErr)
		return nil
	}
	return insertAndWait(ctx, op)
}

// isNotFound reports whether err is a GCP 404 (resource does not exist).
func isNotFound(err error) bool {
	return hasHTTPStatus(err, 404)
}

// isPermissionDenied reports whether err is a GCP 403 (permission denied).
func isPermissionDenied(err error) bool {
	return hasHTTPStatus(err, 403)
}

// grpcCodeMatchesHTTP maps the gRPC status codes the GCP GAPIC clients return
// onto the HTTP statuses hasHTTPStatus is asked about. Only the two codes this
// codebase gates on are mapped; anything else is not a match.
func grpcCodeMatchesHTTP(c codes.Code, httpCode int) bool {
	switch httpCode {
	case 403:
		return c == codes.PermissionDenied
	case 404:
		return c == codes.NotFound
	default:
		return false
	}
}

// hasHTTPStatus inspects err for a matching HTTP status via the two shapes
// the GCP Go SDKs use (googleapi.Error for REST clients, apierror.APIError
// for gRPC/gax clients), falling back to a case-insensitive substring match
// mirroring the bash seed's `grep -qi "403\|permission"` tolerance so fakes
// can simulate either shape with a plain errors.New.
func hasHTTPStatus(err error, code int) bool {
	if err == nil {
		return false
	}
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == code
	}
	var aerr *apierror.APIError
	if errors.As(err, &aerr) {
		if aerr.HTTPCode() == code {
			return true
		}
		// gRPC-backed GAPIC clients — notably the GKE container API used by
		// Phase20 and the down phases — carry no HTTP status at all:
		// HTTPCode() returns -1 and the real code lives on the gRPC status.
		// Checking HTTPCode() alone therefore ALWAYS reports false for them.
		// That silently inverted Phase20's "does this cluster exist?" gate:
		// GetCluster's NotFound was read as a hard error instead of "create
		// it", so `up` could never provision a greenfield cluster (live-hit
		// 2026-07-21 on cluster bnk-scratch). Compute (Phase10) was unaffected
		// because its REST client returns googleapi.Error above.
		if st := aerr.GRPCStatus(); st != nil {
			return grpcCodeMatchesHTTP(st.Code(), code)
		}
		return false
	}
	msg := strings.ToLower(err.Error())
	switch code {
	case 403:
		return strings.Contains(msg, "403") || strings.Contains(msg, "permission")
	case 404:
		return strings.Contains(msg, "404") || strings.Contains(msg, "not found")
	default:
		return false
	}
}
