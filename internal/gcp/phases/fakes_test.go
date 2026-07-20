package phases

import (
	"context"
	"errors"

	compute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	containerpb "cloud.google.com/go/container/apiv1/containerpb"
	gax "github.com/googleapis/gax-go/v2"
	serviceusage "google.golang.org/api/serviceusage/v1"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
)

// errNotFound and errPermissionDenied satisfy hasHTTPStatus's string-fallback
// path (phase10_networks.go) — plain errors are enough since fakes never
// need the real googleapi.Error/apierror.APIError shapes.
var (
	errNotFound         = errors.New("googleapi: got HTTP response code 404 with body: not found")
	errPermissionDenied = errors.New("googleapi: got HTTP response code 403 with body: permission denied")
)

// --- compute.NetworksAPI ---

type fakeNetworks struct {
	existing    map[string]bool
	insertCalls []string
	insertErr   map[string]error
	deleteCalls []string
}

func (f *fakeNetworks) Get(_ context.Context, req *computepb.GetNetworkRequest, _ ...gax.CallOption) (*computepb.Network, error) {
	if f.existing[req.GetNetwork()] {
		return &computepb.Network{Name: strPtr(req.GetNetwork())}, nil
	}
	return nil, errNotFound
}

func (f *fakeNetworks) Insert(_ context.Context, req *computepb.InsertNetworkRequest, _ ...gax.CallOption) (*compute.Operation, error) {
	name := req.GetNetworkResource().GetName()
	f.insertCalls = append(f.insertCalls, name)
	if f.insertErr != nil {
		if err, ok := f.insertErr[name]; ok {
			return nil, err
		}
	}
	return nil, nil
}

func (f *fakeNetworks) Delete(_ context.Context, req *computepb.DeleteNetworkRequest, _ ...gax.CallOption) (*compute.Operation, error) {
	f.deleteCalls = append(f.deleteCalls, req.GetNetwork())
	return nil, nil
}

// --- compute.SubnetworksAPI ---

type fakeSubnetworks struct {
	existing    map[string]bool
	insertCalls []string
	deleteCalls []string
}

func (f *fakeSubnetworks) Get(_ context.Context, req *computepb.GetSubnetworkRequest, _ ...gax.CallOption) (*computepb.Subnetwork, error) {
	if f.existing[req.GetSubnetwork()] {
		return &computepb.Subnetwork{Name: strPtr(req.GetSubnetwork())}, nil
	}
	return nil, errNotFound
}

func (f *fakeSubnetworks) Insert(_ context.Context, req *computepb.InsertSubnetworkRequest, _ ...gax.CallOption) (*compute.Operation, error) {
	f.insertCalls = append(f.insertCalls, req.GetSubnetworkResource().GetName())
	return nil, nil
}

func (f *fakeSubnetworks) Delete(_ context.Context, req *computepb.DeleteSubnetworkRequest, _ ...gax.CallOption) (*compute.Operation, error) {
	f.deleteCalls = append(f.deleteCalls, req.GetSubnetwork())
	return nil, nil
}

// --- compute.FirewallsAPI ---

type fakeFirewalls struct {
	existing    map[string]bool
	insertCalls []string
	deleteCalls []string
}

func (f *fakeFirewalls) Get(_ context.Context, req *computepb.GetFirewallRequest, _ ...gax.CallOption) (*computepb.Firewall, error) {
	if f.existing[req.GetFirewall()] {
		return &computepb.Firewall{Name: strPtr(req.GetFirewall())}, nil
	}
	return nil, errNotFound
}

func (f *fakeFirewalls) Insert(_ context.Context, req *computepb.InsertFirewallRequest, _ ...gax.CallOption) (*compute.Operation, error) {
	f.insertCalls = append(f.insertCalls, req.GetFirewallResource().GetName())
	return nil, nil
}

func (f *fakeFirewalls) Delete(_ context.Context, req *computepb.DeleteFirewallRequest, _ ...gax.CallOption) (*compute.Operation, error) {
	f.deleteCalls = append(f.deleteCalls, req.GetFirewall())
	return nil, nil
}

// --- compute.RoutersAPI ---

type fakeRouters struct {
	// existing maps router name -> the Router that Get should return (nil
	// entry with existing[name]=true is invalid; omit the key entirely for
	// "not found").
	existing map[string]*computepb.Router

	insertCalls []string
	insertErr   map[string]error
	patchCalls  []string
	patchErr    map[string]error
	deleteCalls []string
}

func (f *fakeRouters) Get(_ context.Context, req *computepb.GetRouterRequest, _ ...gax.CallOption) (*computepb.Router, error) {
	if r, ok := f.existing[req.GetRouter()]; ok {
		return r, nil
	}
	return nil, errNotFound
}

func (f *fakeRouters) Insert(_ context.Context, req *computepb.InsertRouterRequest, _ ...gax.CallOption) (*compute.Operation, error) {
	name := req.GetRouterResource().GetName()
	f.insertCalls = append(f.insertCalls, name)
	if f.insertErr != nil {
		if err, ok := f.insertErr[name]; ok {
			return nil, err
		}
	}
	return nil, nil
}

func (f *fakeRouters) Patch(_ context.Context, req *computepb.PatchRouterRequest, _ ...gax.CallOption) (*compute.Operation, error) {
	name := req.GetRouter()
	f.patchCalls = append(f.patchCalls, name)
	if f.patchErr != nil {
		if err, ok := f.patchErr[name]; ok {
			return nil, err
		}
	}
	return nil, nil
}

func (f *fakeRouters) Delete(_ context.Context, req *computepb.DeleteRouterRequest, _ ...gax.CallOption) (*compute.Operation, error) {
	f.deleteCalls = append(f.deleteCalls, req.GetRouter())
	return nil, nil
}

// --- compute.InstancesAPI ---

type fakeInstances struct {
	byName map[string]*computepb.Instance
}

func (f *fakeInstances) Get(_ context.Context, req *computepb.GetInstanceRequest, _ ...gax.CallOption) (*computepb.Instance, error) {
	if inst, ok := f.byName[req.GetInstance()]; ok {
		return inst, nil
	}
	return nil, errNotFound
}

// --- compute.RegionsAPI ---

type fakeRegions struct {
	region *computepb.Region
	err    error
}

func (f *fakeRegions) Get(_ context.Context, _ *computepb.GetRegionRequest, _ ...gax.CallOption) (*computepb.Region, error) {
	return f.region, f.err
}

// --- gcp.ServiceUsageAPI ---

type fakeServiceUsage struct {
	states map[string]string // full resource name -> state ("ENABLED"/"DISABLED")
	err    error
}

func (f *fakeServiceUsage) GetService(_ context.Context, name string) (*serviceusage.GoogleApiServiceusageV1Service, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &serviceusage.GoogleApiServiceusageV1Service{State: f.states[name]}, nil
}

// --- container.GKEAPI ---

type fakeGKE struct {
	clusters  map[string]*containerpb.Cluster
	nodePools map[string]*containerpb.NodePool // keyed by pool name

	createClusterCalls   []string
	createNodePoolCalls  []string
	deleteClusterCalls   []string
	createClusterErr     error
	createNodePoolErr    error
	deleteClusterErr     error
	getOperationStatuses map[string]containerpb.Operation_Status // opName -> status; default DONE
}

func (f *fakeGKE) CreateCluster(_ context.Context, req *containerpb.CreateClusterRequest, _ ...gax.CallOption) (*containerpb.Operation, error) {
	f.createClusterCalls = append(f.createClusterCalls, req.GetCluster().GetName())
	if f.createClusterErr != nil {
		return nil, f.createClusterErr
	}
	if f.clusters == nil {
		f.clusters = map[string]*containerpb.Cluster{}
	}
	f.clusters[req.GetCluster().GetName()] = &containerpb.Cluster{
		Name:   req.GetCluster().GetName(),
		Status: containerpb.Cluster_RUNNING,
	}
	return &containerpb.Operation{Name: "op-create-cluster", Status: containerpb.Operation_DONE}, nil
}

func (f *fakeGKE) GetCluster(_ context.Context, req *containerpb.GetClusterRequest, _ ...gax.CallOption) (*containerpb.Cluster, error) {
	if c, ok := f.clusters[req.GetClusterId()]; ok {
		return c, nil
	}
	return nil, errNotFound
}

func (f *fakeGKE) DeleteCluster(_ context.Context, req *containerpb.DeleteClusterRequest, _ ...gax.CallOption) (*containerpb.Operation, error) {
	f.deleteClusterCalls = append(f.deleteClusterCalls, req.GetClusterId())
	if f.deleteClusterErr != nil {
		return nil, f.deleteClusterErr
	}
	delete(f.clusters, req.GetClusterId())
	return &containerpb.Operation{Name: "op-delete-cluster", Status: containerpb.Operation_DONE}, nil
}

func (f *fakeGKE) CreateNodePool(_ context.Context, req *containerpb.CreateNodePoolRequest, _ ...gax.CallOption) (*containerpb.Operation, error) {
	f.createNodePoolCalls = append(f.createNodePoolCalls, req.GetNodePool().GetName())
	if f.createNodePoolErr != nil {
		return nil, f.createNodePoolErr
	}
	if f.nodePools == nil {
		f.nodePools = map[string]*containerpb.NodePool{}
	}
	f.nodePools[req.GetNodePool().GetName()] = &containerpb.NodePool{
		Name:   req.GetNodePool().GetName(),
		Status: containerpb.NodePool_RUNNING,
	}
	return &containerpb.Operation{Name: "op-create-nodepool-" + req.GetNodePool().GetName(), Status: containerpb.Operation_DONE}, nil
}

func (f *fakeGKE) GetNodePool(_ context.Context, req *containerpb.GetNodePoolRequest, _ ...gax.CallOption) (*containerpb.NodePool, error) {
	if p, ok := f.nodePools[req.GetNodePoolId()]; ok {
		return p, nil
	}
	return nil, errNotFound
}

func (f *fakeGKE) GetOperation(_ context.Context, req *containerpb.GetOperationRequest, _ ...gax.CallOption) (*containerpb.Operation, error) {
	status := containerpb.Operation_DONE
	if f.getOperationStatuses != nil {
		if s, ok := f.getOperationStatuses[req.GetOperationId()]; ok {
			status = s
		}
	}
	return &containerpb.Operation{Name: req.GetOperationId(), Status: status}, nil
}

func strPtr(s string) *string { return &s }

var _ gcp.NetworksAPI = (*fakeNetworks)(nil)
var _ gcp.SubnetworksAPI = (*fakeSubnetworks)(nil)
var _ gcp.FirewallsAPI = (*fakeFirewalls)(nil)
var _ gcp.RoutersAPI = (*fakeRouters)(nil)
var _ gcp.InstancesAPI = (*fakeInstances)(nil)
var _ gcp.RegionsAPI = (*fakeRegions)(nil)
var _ gcp.ServiceUsageAPI = (*fakeServiceUsage)(nil)
var _ gcp.GKEAPI = (*fakeGKE)(nil)
