package ibm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/IBM-Cloud/power-go-client/power/models"
	"github.com/IBM/go-sdk-core/v5/core"
	"github.com/konflux-ci/multi-platform-controller/pkg/config"
	v1 "k8s.io/api/core/v1"
	types2 "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	serviceNameIndex     = 4
	locationIndex        = 5
	serviceInstanceIndex = 7
)

// retrieveInstanceIP returns a string representing the IP address of instance instanceID.
// The function validates the IP address format before returning it.
// Validation ensures the IP is in valid IPv4 dotted decimal notation.
//
// Returns:
// - string: The validated IP address (ExternalIP if available, otherwise IPAddress)
// - error: If no networks found, no IP address found, or IP format is invalid
func retrieveInstanceIp(instanceID string, networks []*models.PVMInstanceNetwork) (string, error) {
	if len(networks) == 0 {
		return "", fmt.Errorf("no networks found for Power Systems VM %s", instanceID)
	}
	network := networks[0]
	if network == nil {
		return "", fmt.Errorf("network entry is nil for Power Systems VM %s", instanceID)
	}

	// Determine which IP to use (prefer ExternalIP over IPAddress)
	var ip string
	if network.ExternalIP != "" {
		ip = network.ExternalIP
	} else if network.IPAddress != "" {
		ip = network.IPAddress
	} else {
		return "", fmt.Errorf("no IP address found for Power Systems VM %s", instanceID)
	}

	// Validate IP format before returning
	if err := config.ValidateIPFormat(ip); err != nil {
		return "", fmt.Errorf("invalid IP address format for Power Systems VM %s: %w", instanceID, err)
	}

	return ip, nil
}

// createAuthenticatedBaseService generates a base communication service with an API key-based IAM (Identity and Access
// Management) authenticator.
func (pw IBMPowerDynamicConfig) createAuthenticatedBaseService(ctx context.Context, kubeClient client.Client) (*core.BaseService, error) {
	apiKey := ""
	if kubeClient == nil { // Get API key from an environment variable
		apiKey = os.Getenv("IBM_CLOUD_API_KEY")
	} else { // Get API key from pw's Kubernetes Secret
		secret := v1.Secret{}
		namespacedSecret := types2.NamespacedName{Name: pw.Secret, Namespace: pw.SystemNamespace}
		err := kubeClient.Get(ctx, namespacedSecret, &secret)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve the secret %s from the Kubernetes client: %w", namespacedSecret, err)
		}

		apiKeyByte, ok := secret.Data["api-key"]
		if !ok {
			return nil, fmt.Errorf("the secret %s did not have an API key field", namespacedSecret)
		}
		apiKey = string(apiKeyByte)
	}

	serviceOptions := &core.ServiceOptions{
		URL: pw.Url,
		Authenticator: &core.IamAuthenticator{
			ApiKey: apiKey,
		},
	}
	baseService, err := core.NewBaseService(serviceOptions)
	return baseService, err
}

// parseCRN returns the service instance of pw's Cloud Resource Name.
// See the [IBM CRN docs](https://cloud.ibm.com/docs/account?topic=account-crn#service-instance-crn)
// for more information on CRN formatting.
func (pw IBMPowerDynamicConfig) parseCRN() (string, error) {
	if !strings.HasPrefix(pw.CRN, "crn:") {
		return "", errors.New("invalid CRN: must start with 'crn:'")
	}

	crnSegments := strings.Split(pw.CRN, ":")

	// To prevent index-out-of-bounds panic if this string gets truncated, we need to verify the correct length - 10.
	//Like the commandments.
	if len(crnSegments) != 10 {
		return "", fmt.Errorf("invalid CRN format: expected 10 segments, but got %d", len(crnSegments))
	}

	// Verify this is definitely a ppc - see:
	// [IBM Cloud global catalog service](https://globalcatalog.cloud.ibm.com/search?noLocations=true&q=power-iaas)
	if crnSegments[serviceNameIndex] != "power-iaas" {
		return "", fmt.Errorf("invalid CRN service name: expected 'power-iaas', but got '%s'", crnSegments[serviceNameIndex])
	}

	if crnSegments[locationIndex] == "global" {
		return "", errors.New("this resource is global and has no service instance")
	}
	serviceInstance := crnSegments[serviceInstanceIndex]
	if serviceInstance == "" {
		return "", errors.New("the service instance is null")
	}

	return serviceInstance, nil
}
