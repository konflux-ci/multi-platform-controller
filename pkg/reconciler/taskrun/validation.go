package taskrun

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

var (
	errInvalidPlatformFormat    = errors.New("platform must be in format 'label/label' where each label follows Kubernetes RFC 1035 label name format")
	errMissingPlatformParameter = errors.New("PLATFORM parameter not found in TaskRun parameters")
	// TODO: comment-out when it's time for KFLUXINFRA-2328
	//errInvalidIPFormat          = errors.New("value must be a valid IP address in dotted decimal notation")

	// IBM resource validation errors
	errIBMHostSecretEmpty            = errors.New("host secret value cannot be empty")
	errIBMHostSecretPlatformMismatch = errors.New("host secret key and value must contain matching platform substring")

	maxInstancesValue = 250
	// Maximum allocation timeout value in seconds (20 minutes)
	maxAllocationTimeout = 1200
	// Maximum static host concurrency
	//maxStaticConcurrency = 8 TODO: uncomment in the next PR
	// Maximum pool host age in minutes (24 hours)
	//maxPoolHostAge = 1440 TODO: uncomment in the next PR
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

// validateNonZeroPositiveNumber validates a string represents a valid integer within the specified range
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
// - error: If value is invalid or out of range
func validateNonZeroPositiveNumber(value string, maxValue int) (int, error) {
	num, err := strconv.Atoi(value)
	if err != nil {
		return -1, fmt.Errorf("invalid value '%s': must be a valid integer between 1 and %d: %w", value, maxValue, err)
	}
	if num < 1 || num > maxValue {
		return -1, fmt.Errorf("invalid value '%s': must be a valid integer between 1 and %d", value, maxValue)
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
// - error: If value is invalid or out of range
func validateMaxInstances(value string) (int, error) {
	result, err := validateNonZeroPositiveNumber(value, maxInstancesValue)
	if err != nil {
		return -1, fmt.Errorf("invalid max-instances: %w", err)
	}
	return result, nil
}

// validateMaxAllocationTimeout validates a string represents a valid allocation timeout value
// Uses the global maxAllocationTimeout as the upper limit
//
// Parameters:
// - value: The string value to validate
//
// Returns:
// - int: The validated integer value
// - error: If value is invalid or out of range
func validateMaxAllocationTimeout(value string) (int, error) {
	result, err := validateNonZeroPositiveNumber(value, maxAllocationTimeout)
	if err != nil {
		return -1, fmt.Errorf("invalid allocation-timeout: %w", err)
	}
	return result, nil
}

// obfuscateIP takes an IP address and returns it with the first three octets replaced with asterisks
// Example: "192.168.1.1" becomes "***.***.***.1"
func obfuscateIP(ip string) string {
	parts := strings.Split(ip, ".")
	return "***.***.***." + parts[3]
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
// - Key format: "<whatever>-<instance-type>-<platform>" (e.g., "linux-m2xlarge-arm64")
// - Value format: "<whatever>-<platform>-<instance-type>" (e.g., "prod-arm64-m2xlarge")
// - Platform must match between key and value (arm64/amd64)
// - Instance type must match between key and value
//
// Returns:
//   - nil if validation passes
//   - a descriptive error if the validation fails or if the inputs are malformed, and nil if
//     the validation succeeds.
func validateDynamicInstanceTag(key, value string) error {
	// Parse and normalize the platform and instance type from the key, then from the value.
	keyPlatform, keyInstanceType, err := parseDynamicHostInstanceTypeKey(key)
	if err != nil {
		return err
	}

	valuePlatform, valueInstanceType, err := parseDynamicHostInstanceTypeValue(value)
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

	// Platform is everything after the last dash.
	platform = platformConfigName[lastDash+1:]
	if firstDash == lastDash {
		// Simple platform with no instance type (e.g., "linux-arm64")
		// Platform is everything after the dash, no instance type
		platform = platformConfigName[lastDash+1:]
		return platform, "", nil
	}

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

// parseDynamicHostInstanceTypeValue extracts the platform and instance type from the config value string
// This function parses the config value format and normalizes multi-part instance types.
//
// Parameters:
// - value: The config value string to parse
//
// Returns:
// - string: The extracted platform
// - string: The normalized instance type
// - error: Parsing error if value format is invalid
func parseDynamicHostInstanceTypeValue(value string) (platform, instanceType string, err error) {
	parts := strings.Split(value, "-")

	// We need at least 2 parts - a prefix and a platform
	// If there are only 2 parts, it's a simple value (e.g., "prod-arm64") with no instance type
	if len(parts) < 2 {
		return "", "", errors.New("invalid value format: must be '<prefix>-<platform>' or '<prefix>-<platform>-<instance_type>'")
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
