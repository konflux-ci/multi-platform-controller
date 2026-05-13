package ibm

import (
	"context"

	"github.com/IBM/go-sdk-core/v5/core"
	"github.com/IBM/vpc-go-sdk/vpcv1"
)

// mockVpcClient satisfies the vpcAPI interface for testing.
// Output/Err fields control return values.
type mockVpcClient struct {
	ListSubnetsOutput    *vpcv1.SubnetCollection
	ListSubnetsErr       error
	ListKeysOutput       *vpcv1.KeyCollection
	ListKeysErr          error
	ListVpcsOutput       *vpcv1.VPCCollection
	ListVpcsErr          error
	ListInstancesOutput  *vpcv1.InstanceCollection
	ListInstancesErr     error
	CreateInstanceOutput *vpcv1.Instance
	CreateInstanceErr    error
	GetInstanceOutput    *vpcv1.Instance
	GetInstanceResp      *core.DetailedResponse
	GetInstanceErr       error
	DeleteInstanceErr    error

	ListNetIfaceFloatingIpsOutput *vpcv1.FloatingIPUnpaginatedCollection
	ListNetIfaceFloatingIpsErr    error
	ListFloatingIpsOutput         *vpcv1.FloatingIPCollection
	ListFloatingIpsErr            error
	AddNetIfaceFloatingIPOutput   *vpcv1.FloatingIP
	AddNetIfaceFloatingIPErr      error
	CreateFloatingIPOutput        *vpcv1.FloatingIP
	CreateFloatingIPErr           error
}

func (m *mockVpcClient) ListSubnets(_ *vpcv1.ListSubnetsOptions) (*vpcv1.SubnetCollection, *core.DetailedResponse, error) {
	return m.ListSubnetsOutput, nil, m.ListSubnetsErr
}

func (m *mockVpcClient) ListKeys(_ *vpcv1.ListKeysOptions) (*vpcv1.KeyCollection, *core.DetailedResponse, error) {
	return m.ListKeysOutput, nil, m.ListKeysErr
}

func (m *mockVpcClient) ListVpcs(_ *vpcv1.ListVpcsOptions) (*vpcv1.VPCCollection, *core.DetailedResponse, error) {
	return m.ListVpcsOutput, nil, m.ListVpcsErr
}

func (m *mockVpcClient) ListInstances(_ *vpcv1.ListInstancesOptions) (*vpcv1.InstanceCollection, *core.DetailedResponse, error) {
	return m.ListInstancesOutput, nil, m.ListInstancesErr
}

func (m *mockVpcClient) CreateInstance(_ *vpcv1.CreateInstanceOptions) (*vpcv1.Instance, *core.DetailedResponse, error) {
	return m.CreateInstanceOutput, nil, m.CreateInstanceErr
}

func (m *mockVpcClient) GetInstance(_ *vpcv1.GetInstanceOptions) (*vpcv1.Instance, *core.DetailedResponse, error) {
	return m.GetInstanceOutput, m.GetInstanceResp, m.GetInstanceErr
}

func (m *mockVpcClient) GetInstanceWithContext(_ context.Context, _ *vpcv1.GetInstanceOptions) (*vpcv1.Instance, *core.DetailedResponse, error) {
	return m.GetInstanceOutput, m.GetInstanceResp, m.GetInstanceErr
}

func (m *mockVpcClient) DeleteInstanceWithContext(_ context.Context, _ *vpcv1.DeleteInstanceOptions) (*core.DetailedResponse, error) {
	return nil, m.DeleteInstanceErr
}

func (m *mockVpcClient) ListInstanceNetworkInterfaceFloatingIps(_ *vpcv1.ListInstanceNetworkInterfaceFloatingIpsOptions) (*vpcv1.FloatingIPUnpaginatedCollection, *core.DetailedResponse, error) {
	return m.ListNetIfaceFloatingIpsOutput, nil, m.ListNetIfaceFloatingIpsErr
}

func (m *mockVpcClient) ListFloatingIps(_ *vpcv1.ListFloatingIpsOptions) (*vpcv1.FloatingIPCollection, *core.DetailedResponse, error) {
	return m.ListFloatingIpsOutput, nil, m.ListFloatingIpsErr
}

func (m *mockVpcClient) AddInstanceNetworkInterfaceFloatingIP(_ *vpcv1.AddInstanceNetworkInterfaceFloatingIPOptions) (*vpcv1.FloatingIP, *core.DetailedResponse, error) {
	return m.AddNetIfaceFloatingIPOutput, nil, m.AddNetIfaceFloatingIPErr
}

func (m *mockVpcClient) CreateFloatingIP(_ *vpcv1.CreateFloatingIPOptions) (*vpcv1.FloatingIP, *core.DetailedResponse, error) {
	return m.CreateFloatingIPOutput, nil, m.CreateFloatingIPErr
}
