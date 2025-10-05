package taskrun

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	// alias for clarity, otherwise it'll look like aws.ActuallyWeThoughtAboutThisOne()
	ec2Helpers "github.com/konflux-ci/multi-platform-controller/pkg/aws"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	errInvalidPlatformFormat    = errors.New("platform must be in format 'label/label' where each label follows Kubernetes RFC 1035 label name format")
	errMissingPlatformParameter = errors.New("PLATFORM parameter not found in TaskRun parameters")
	errInvalidNumericValue      = errors.New("value must be a valid integer between 1 and 100")
	errInvalidIPFormat          = errors.New("value must be a valid IP address in dotted decimal notation")

	// AWS resource validation errors - standardized to avoid exposing resource details in logs
	errAWSClientCreation    = errors.New("failed to create AWS EC2 client")
	errSecurityGroupInvalid = errors.New("security group not found or inaccessible")
	errSubnetInvalid        = errors.New("subnet not found or inaccessible")
	errKeyPairInvalid       = errors.New("key pair not found or inaccessible")
	errAMIInvalid           = errors.New("AMI not found or not available")

	// IBM resource validation errors
	errIBMHostSecretEmpty            = errors.New("host secret value cannot be empty")
	errIBMHostSecretPlatformMismatch = errors.New("host secret key and value must contain matching platform substring")

	maxInstancesValue = 250
	// Maximum allocation timeout value in seconds (20 minutes)
	maxAllocationTimeout = 1200
	// Maximum static host concurrency
	maxStaticConcurrency = 8
	// Maximum pool host age in minutes (24 hours)
	maxPoolHostAge = 1440

	// Cache flag for validated AWS resources to avoid repeated validation
	mpcAWSResourcesValidated = false
)

// validatePlatformFormat validates a platform string according to the controller's rules
// Validation rules:
// - Exceptional platforms: "local", "localhost", "linux/x86_64" bypass format validation
// - Standard platforms: must be "label/label" format where each label follows RFC 1035
// - RFC 1035 compliance: lowercase alphanumeric characters and hyphens, start/end with alphanumeric
//
// Returns:
// - nil if platform is valid
// - errInvalidPlatformFormat if format is incorrect
func validatePlatformFormat(platform string) error {
	// Check for exceptional platforms that bypass standard validation
	if platform == "local" || platform == "localhost" || platform == "linux/x86_64" {
		return nil
	}

	// Validate platform format: must be "label/label" where each label follows RFC 1035
	parts := strings.Split(platform, "/")
	if len(parts) != 2 {
		return errInvalidPlatformFormat
	}

	// Validate each part against RFC 1035 label name rules
	for _, part := range parts {
		if p := validation.IsDNS1035Label(part); len(p) != 0 {
			return errInvalidPlatformFormat
		}
	}

	return nil
}

// validatePlatform extracts and validates the platform parameter from a TaskRun
// This function combines platform extraction and format validation in a single operation.
// It first extracts the PLATFORM parameter from the TaskRun, then validates its format.
//
// Parameters:
// - tr: The TaskRun object to extract and validate the platform from
//
// Returns:
// - string: The validated platform value if found and valid
// - error: errMissingPlatformParameter if PLATFORM parameter not found, or errInvalidPlatformFormat if format is invalid
func validatePlatform(tr *tektonapi.TaskRun) (string, error) {
	platform, err := extractPlatform(tr)
	if err != nil {
		return "", err
	}

	if err := validatePlatformFormat(platform); err != nil {
		return "", err
	}

	return platform, nil
}

// extractPlatform extracts the platform parameter value from a TaskRun's parameters
// This is a helper function for platform validation that searches through the TaskRun's
// parameter list to find the PLATFORM parameter value.
//
// Parameters:
// - tr: The TaskRun object to extract the platform from
//
// Returns:
// - string: The platform value if found
// - error: errMissingPlatformParameter if the PLATFORM parameter is not found
func extractPlatform(tr *tektonapi.TaskRun) (string, error) {
	for _, p := range tr.Spec.Params {
		if p.Name == PlatformParam {
			return p.Value.StringVal, nil
		}
	}
	return "", errMissingPlatformParameter
}

// validateNumericValue validates a string represents a valid integer within the specified range
// Validation rules:
// - Must be a valid integer parseable by strconv.Atoi
// - Must be between 1 and maxValue inclusive
//
// Parameters:
// - value: The string value to validate
// - maxValue: The maximum allowed value
//
// Returns:
// - int: The validated integer value
// - error: errInvalidNumericValue if value is invalid or out of range
func validateNumericValue(value string, maxValue int) (int, error) {
	num, err := strconv.Atoi(value)
	if err != nil || num != max(0, min(num, maxValue)) {
		return 0, errInvalidNumericValue
	}
	return num, nil
}

// validateMaxInstances validates a string represents a valid max-instances value
// Uses the global maxInstancesValue as the upper limit
//
// Parameters:
// - value: The string value to validate
//
// Returns:
// - int: The validated integer value
// - error: errInvalidNumericValue if value is invalid or out of range
func validateMaxInstances(value string) (int, error) {
	return validateNumericValue(value, maxInstancesValue)
}

// validateMaxAllocationTimeout validates a string represents a valid allocation timeout value
// Uses the global maxAllocationTimeout as the upper limit
//
// Parameters:
// - value: The string value to validate
//
// Returns:
// - int: The validated integer value
// - error: errInvalidNumericValue if value is invalid or out of range
func validateMaxAllocationTimeout(value string) (int, error) {
	return validateNumericValue(value, maxAllocationTimeout)
}

// obfuscateIP takes an IP address and returns it with the first three octets replaced with asterisks
// Example: "192.168.1.1" becomes "***.***.***.1"
func obfuscateIP(ip string) string {
	parts := strings.Split(ip, ".")
	return fmt.Sprintf("***.***.***.%s", parts[3])
}

// validateIP validates that a string represents a valid and reachable IP address
// Uses Go's standard net.ParseIP function to validate IP format and attempts to connect to port 22.
// This function assumes IPv4 addresses are being validated.
// Validation rules:
// - Must be a valid IPv4 address in dotted decimal notation (e.g., "192.168.1.1")
// - Must be reachable on port 22 (SSH)
//
// Returns:
// - nil if value is a valid and reachable IP address
// - errInvalidIPFormat if value is not a valid IP address
// - dynamic error if IP is valid but host is unreachable (includes obfuscated IP)
func validateIP(value string) error {
	ip := net.ParseIP(value)
	if ip != nil {
		// IP format is valid, now check reachability
		if err := ec2Helpers.PingIPAddress(value); err != nil {
			obfuscatedIP := obfuscateIP(value)
			return fmt.Errorf("IP address is valid but host %s is unreachable", obfuscatedIP)
		}
		return nil
	}
	return errInvalidIPFormat
}

// validateIBMHostSecret validates IBM host secret configuration key-value pairs
// Validation rules:
// - Secret value cannot be empty
// - Key and value must contain matching platform substring (either "s390x" or "ppc64le")
// - No specific structure requirements - platform can appear anywhere in key or value
//
// Returns:
// - nil if validation passes
// - errIBMHostSecretEmpty if value is empty
// - errIBMHostSecretPlatformMismatch if no matching platform substring found
func validateIBMHostSecret(key, value string) error {
	if strings.TrimSpace(value) == "" {
		return errIBMHostSecretEmpty
	}

	platforms := []string{"s390x", "ppc64le"}
	for _, platform := range platforms {
		if strings.Contains(key, platform) && strings.Contains(value, platform) {
			return nil
		}
	}

	return errIBMHostSecretPlatformMismatch
}

// validateDynamicInstanceTag validates dynamic AWS host instance-tag configuration
// - Key format: "<host type>.<whatever>-<instance-type>-<platform>" (e.g., "dynamic.linux-m2xlarge-arm64")
// - Value format: "<whatever>-<platform>-<instance-type>" (e.g., "prod-arm64-m2xlarge")
// - Platform must match between key and value (arm64/amd64)
// - Instance type must match between key and value
//
// Returns:
//   - nil if validation passes
//   - a descriptive error if the validation fails or if the inputs are malformed, and nil if
//     the validation succeeds.
func validateDynamicInstanceTag(key, value string) error {
	// Parse and normalize the platform and instance type from the key, then from the key.
	keyPlatform, keyInstanceType, err := parseDynamicHostInstanceTypeKey(key)
	if err != nil {
		return err
	}

	valuePlatform, valueInstanceType, err := parseDynamicHostInstanceTypValue(value)
	if err != nil {
		return err
	}

	// Platforms must be an exact match.
	if keyPlatform != valuePlatform {
		return fmt.Errorf("platform mismatch: key has '%s', value has '%s'", keyPlatform, valuePlatform)
	}
	// Instance types must be an exact match.
	if keyInstanceType != valueInstanceType {
		return fmt.Errorf("instance type mismatch: key has '%s', value has '%s'", keyInstanceType, valueInstanceType)
	}
	return nil
}

// parseDynamicHostInstanceTypeKey extracts the platform and instance type from the platform config name.
// This function parses the simplified platform config name format and normalizes multi-part instance types.
//
// Parameters:
// - platformConfigName: The platform config name (e.g., "linux-arm64" or "linux-d160-m4xlarge-arm64")
//
// Returns:
// - string: The extracted platform
// - string: The normalized instance type
// - error: Parsing error if format is invalid
func parseDynamicHostInstanceTypeKey(platformConfigName string) (platform, instanceType string, err error) {
	firstDash := strings.Index(platformConfigName, "-")
	lastDash := strings.LastIndex(platformConfigName, "-")

	if firstDash == -1 || lastDash == -1 {
		return "", "", fmt.Errorf("invalid platform config name: no dashes found in '%s'", platformConfigName)
	}

	if firstDash == lastDash {
		// Simple platform with no instance type (e.g., "linux-arm64")
		// Platform is everything after the dash, no instance type
		platform = platformConfigName[lastDash+1:]
		return platform, "", nil
	}

	// Platform is everything after the last dash.
	platform = platformConfigName[lastDash+1:]
	// Instance type is everything between the first and last dash.
	instanceType = platformConfigName[firstDash+1 : lastDash]

	// Normalize the instance type if it contains multiple parts
	instanceParts := strings.Split(instanceType, "-")
	if len(instanceParts) > 1 {
		sort.Strings(instanceParts)
		instanceType = strings.Join(instanceParts, "-")
	}

	return platform, instanceType, nil
}

// parseDynamicHostInstanceTypValue extracts the platform and instance type from the config value string
// This function parses the config value format and normalizes multi-part instance types.
//
// Parameters:
// - value: The config value string to parse
//
// Returns:
// - string: The extracted platform
// - string: The normalized instance type
// - error: Parsing error if value format is invalid
func parseDynamicHostInstanceTypValue(value string) (platform, instanceType string, err error) {
	parts := strings.Split(value, "-")

	// We need at least 2 parts - a prefix and a platform
	// If there are only 2 parts, it's a simple value (e.g., "prod-arm64") with no instance type
	if len(parts) < 2 {
		return "", "", fmt.Errorf("invalid value format: must be '<prefix>-<platform>' or '<prefix>-<platform>-<instance_type>'")
	}

	// Platform is always the second part (index 1)
	platform = parts[1]

	// If there are only 2 parts, there's no instance type
	if len(parts) == 2 {
		return platform, "", nil
	}

	// Instance type is all remaining parts (from index 2 onwards)
	instanceParts := parts[2:]

	// Normalize if there's more than one component to the instance type
	if len(instanceParts) > 1 {
		sort.Strings(instanceParts)
	}
	instanceType = strings.Join(instanceParts, "-")

	return platform, instanceType, nil
}

// AWSResourceValidationConfig holds configuration for AWS resource validation
type AWSResourceValidationConfig struct {
	Region          string
	AMI             string
	SecurityGroupId string
	SubnetId        string
	KeyName         string
	AWSSecret       string
	SystemNamespace string
}

// validateAWSResources validates AWS resources exist in the specified account/region
// Validates security groups, subnets, and key pairs once (with caching), AMIs always validated
// Returns error if any required resources are not found or cannot be accessed
func validateAWSResources(ctx context.Context, kubeClient client.Client, config AWSResourceValidationConfig) error {
	// Create AWS EC2 configuration so we can utilize
	awsConfig := ec2Helpers.AWSEc2DynamicConfig{
		Region:          config.Region,
		Secret:          config.AWSSecret,
		SystemNamespace: config.SystemNamespace,
	}

	// Create EC2 client once and reuse for all validations
	ec2Client, err := awsConfig.CreateClient(kubeClient, ctx)
	if err != nil {
		return errAWSClientCreation
	}

	// Validate one-time resources (security groups, subnets, key pairs) only if not already validated
	if !mpcAWSResourcesValidated {
		// Validate security group (one-time validation)
		if config.SecurityGroupId != "" {
			if err := validateSecurityGroup(ctx, ec2Client, config.SecurityGroupId); err != nil {
				return err
			}
		}

		// Validate subnet (one-time validation)
		if config.SubnetId != "" {
			if err := validateSubnet(ctx, ec2Client, config.SubnetId); err != nil {
				return err
			}
		}

		// Validate key pair (one-time validation)
		if config.KeyName != "" {
			if err := validateKeyPair(ctx, ec2Client, config.KeyName); err != nil {
				return err
			}
		}

		// Mark these resources as validated
		mpcAWSResourcesValidated = true
	}

	// Always validate AMI (no caching)
	if config.AMI != "" {
		if err := validateAMI(ctx, ec2Client, config.AMI); err != nil {
			return err
		}
	}

	return nil
}

// validateSecurityGroup validates that a security group exists
func validateSecurityGroup(ctx context.Context, ec2Client *ec2.Client, securityGroupId string) error {
	input := &ec2.DescribeSecurityGroupsInput{
		GroupIds: []string{securityGroupId},
	}

	result, err := ec2Client.DescribeSecurityGroups(ctx, input)
	if err != nil || len(result.SecurityGroups) == 0 {
		return errSecurityGroupInvalid
	}

	return nil
}

// validateSubnet validates that a subnet exists
func validateSubnet(ctx context.Context, ec2Client *ec2.Client, subnetId string) error {
	input := &ec2.DescribeSubnetsInput{
		SubnetIds: []string{subnetId},
	}

	result, err := ec2Client.DescribeSubnets(ctx, input)
	if err != nil || len(result.Subnets) == 0 {
		return errSubnetInvalid
	}

	return nil
}

// validateKeyPair validates that a key pair exists
func validateKeyPair(ctx context.Context, ec2Client *ec2.Client, keyName string) error {
	input := &ec2.DescribeKeyPairsInput{
		KeyNames: []string{keyName},
	}

	result, err := ec2Client.DescribeKeyPairs(ctx, input)
	if err != nil || len(result.KeyPairs) == 0 {
		return errKeyPairInvalid
	}

	return nil
}

// validateAMI validates that an AMI exists and is available
func validateAMI(ctx context.Context, ec2Client *ec2.Client, amiId string) error {
	input := &ec2.DescribeImagesInput{
		ImageIds: []string{amiId},
	}

	result, err := ec2Client.DescribeImages(ctx, input)
	if err != nil || len(result.Images) == 0 {
		return errAMIInvalid
	}

	// Check if AMI is available
	if string(result.Images[0].State) != "available" {
		return errAMIInvalid
	}

	return nil
}
