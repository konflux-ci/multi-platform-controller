package ibm

import (
	"context"

	"github.com/IBM/go-sdk-core/v5/core"
	"github.com/IBM/vpc-go-sdk/vpcv1"
)

// vpcAPI is the subset of the IBM VPC client API used by this package.
// The real *vpcv1.VpcV1 satisfies this interface; tests can substitute a mock.
type vpcAPI interface {
	ListSubnets(options *vpcv1.ListSubnetsOptions) (*vpcv1.SubnetCollection, *core.DetailedResponse, error)
	ListKeys(options *vpcv1.ListKeysOptions) (*vpcv1.KeyCollection, *core.DetailedResponse, error)
	ListVpcs(options *vpcv1.ListVpcsOptions) (*vpcv1.VPCCollection, *core.DetailedResponse, error)
	ListInstances(options *vpcv1.ListInstancesOptions) (*vpcv1.InstanceCollection, *core.DetailedResponse, error)
	CreateInstance(options *vpcv1.CreateInstanceOptions) (*vpcv1.Instance, *core.DetailedResponse, error)
	GetInstance(options *vpcv1.GetInstanceOptions) (*vpcv1.Instance, *core.DetailedResponse, error)
	GetInstanceWithContext(ctx context.Context, options *vpcv1.GetInstanceOptions) (*vpcv1.Instance, *core.DetailedResponse, error)
	DeleteInstanceWithContext(ctx context.Context, options *vpcv1.DeleteInstanceOptions) (*core.DetailedResponse, error)
	ListInstanceNetworkInterfaceFloatingIps(options *vpcv1.ListInstanceNetworkInterfaceFloatingIpsOptions) (*vpcv1.FloatingIPUnpaginatedCollection, *core.DetailedResponse, error)
	ListFloatingIps(options *vpcv1.ListFloatingIpsOptions) (*vpcv1.FloatingIPCollection, *core.DetailedResponse, error)
	AddInstanceNetworkInterfaceFloatingIP(options *vpcv1.AddInstanceNetworkInterfaceFloatingIPOptions) (*vpcv1.FloatingIP, *core.DetailedResponse, error)
	CreateFloatingIP(options *vpcv1.CreateFloatingIPOptions) (*vpcv1.FloatingIP, *core.DetailedResponse, error)
}
