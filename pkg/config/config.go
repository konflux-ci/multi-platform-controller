package config

import (
	"fmt"
	"strings"
)

var (
	// Default allocation timeout for dynamic platforms in seconds (10 minutes)
	defaultAllocationTimeout = 600
	defaultCheckInterval     = 60

	// Default concurrency for static hosts
	defaultStaticHostsConcurrency = 0
)

type PlatformType string

// platform type enum
const (
	PlatformTypeLocal       PlatformType = "local"
	PlatformTypeDynamic     PlatformType = "dynamic"
	PlatformTypeDynamicPool PlatformType = "dynamic pool"
)

// parsePlatformList parses and validates a comma-separated list of platforms
// This function splits the input string by commas, validates each platform against the RFC 1035 label format,
// and returns a slice of valid platform strings. It handles trailing commas gracefully.
//
// Validation rules:
// - Empty strings are allowed only at the end of the list (from trailing commas)
// - Empty strings in the middle of the list result in an error
// - Each platform must pass validatePlatformFormat validation
//
// Parameters:
// - platformList: The comma-separated string of platforms to parse
// - pType: The platform type for error messages (e.g., PlatformTypeLocal, PlatformTypeDynamic)
//
// Returns:
// - []string: Slice of validated platform strings
// - error: Validation error if any platform is invalid or empty in middle of list
func ParsePlatformList(platformList string, pType PlatformType) ([]string, error) {
	if platformList == "" {
		return []string{}, nil
	}

	platforms := strings.Split(platformList, ",")
	result := make([]string, 0, len(platforms))
	for i, platform := range platforms {
		platform = strings.TrimSpace(platform)
		if platform == "" {
			// Allow empty strings only at the end (from trailing commas) this is because both dynamic-platforms and
			// local-platforms - both values end in a trailing ",". This code makes sure that a trailing comma is a
			// trailing comma and not an invalid space in the middle.of the platform list string.
			if i == len(platforms)-1 {
				continue
			}
			return nil, fmt.Errorf("invalid %s platform '': empty platform in middle of list", pType)
		}
		if err := validatePlatformFormat(platform); err != nil {
			return nil, fmt.Errorf("invalid %s platform '%s': %w", pType, platform, err)
		}
		result = append(result, platform)
	}
	return result, nil
}

// parseDynamicRequiredTypeField extracts and validates the required type field from dynamic platform configuration
// This helper function is specifically for dynamic and dynamic pool platform parsers, validating cloud provider type
// configuration.
//
// Parameters:
// - data: The ConfigMap data map containing platform configuration
// - prefix: The configuration prefix (e.g., "dynamic.linux-amd64.")
// - platform: The platform name for error messages
// - platformType: The platform type for error messages ("dynamic platform" or "dynamic pool platform")
//
// Returns:
// - string: The cloud provider type value
// - error: Error if field is missing
func parseDynamicRequiredTypeField(data map[string]string, prefix, platform, platformType string) (string, error) {
	if typeName := data[prefix+"type"]; typeName != "" {
		return typeName, nil
	}
	return "", fmt.Errorf("%s '%s': type field is required", platformType, platform)
}

// parseDynamicRequiredMaxInstancesField extracts and validates the required max-instances field from dynamic platform configuration
// This helper function is specifically for dynamic and dynamic pool platform parsers, validating the maximum number of
// cloud instances that can be allocated.
//
// Parameters:
// - data: The ConfigMap data map containing platform configuration
// - prefix: The configuration prefix (e.g., "dynamic.linux-amd64.")
// - platform: The platform name for error messages
// - platformType: The platform type for error messages ("dynamic platform" or "dynamic pool platform")
//
// Returns:
// - int: The validated max-instances value (>= 1, no upper limit)
// - error: Error if field is missing or invalid
func parseDynamicRequiredMaxInstancesField(data map[string]string, prefix, platform, platformType string) (int, error) {
	if maxInstancesStr := data[prefix+"max-instances"]; maxInstancesStr != "" {
		maxInstances, err := validateNonZeroPositiveNumber(maxInstancesStr)
		if err != nil {
			return 0, fmt.Errorf("%s '%s': invalid max-instances '%s': %w", platformType, platform, maxInstancesStr, err)
		}
		return maxInstances, nil
	}
	return 0, fmt.Errorf("%s '%s': max-instances field is required", platformType, platform)
}

// parseDynamicOptionalInstanceTagField extracts and validates the optional instance-tag field from dynamic platform configuration
// This helper function is specifically for dynamic and dynamic pool platform parsers, validating cloud instance tags
// for cost control and resource management.
//
// Parameters:
// - data: The ConfigMap data map containing platform configuration
// - prefix: The configuration prefix (e.g., "dynamic.linux-amd64.")
// - platformConfigName: The platform config name with dashes (e.g., "linux-amd64")
// - platform: The platform name for error messages
// - platformType: The platform type for error messages ("dynamic platform" or "dynamic pool platform")
//
// Returns:
// - string: The instance tag value (empty string if not provided)
// - error: Error if validation fails
func parseDynamicOptionalInstanceTagField(data map[string]string, prefix, platformConfigName, platform, platformType string) (string, error) {
	if instanceTag := data[prefix+"instance-tag"]; instanceTag != "" {
		if err := validateDynamicInstanceTag(platformConfigName, instanceTag); err != nil {
			return "", fmt.Errorf("%s '%s': invalid instance-tag '%s': %w", platformType, platform, instanceTag, err)
		}
		return instanceTag, nil
	}
	return "", nil
}

// parseRequiredSSHSecretField extracts and validates the required ssh-secret field from platform configuration
// This helper function validates SSH secret configuration for cloud instances across all platform types.
// It receives the already-validated cloud provider type from the config struct being built.
// Validation differs based on cloud provider type:
// - AWS: Validates non-empty value (any non-empty trimmed value is valid)
// - IBM (ibmz, ibmp): Uses validateIBMHostSecret for additional platform-specific validation
// - Other types: Returns error (currently, only "aws", "ibmz", and "ibmp" are supported)
//
// Parameters:
// - data: The ConfigMap data map containing platform configuration
// - prefix: The configuration prefix (e.g., "dynamic.linux-amd64.")
// - platform: The platform name for error messages
// - platformType: The platform type for error messages (e.g., "dynamic platform" or "dynamic pool platform")
// - cloudProviderType: The cloud provider type from the config struct ("aws", "ibmz", or "ibmp")
//
// Returns:
// - string: The SSH secret name
// - error: Error if field is missing, empty, or fails validation
func parseRequiredSSHSecretField(data map[string]string, prefix, platform, platformType, cloudProviderType string) (string, error) {
	sshSecret := strings.TrimSpace(data[prefix+"ssh-secret"])
	if sshSecret == "" {
		return "", fmt.Errorf("%s '%s': ssh-secret field is required", platformType, platform)
	}

	switch cloudProviderType {
	case "aws":
		// For AWS platforms, the trimmed non-empty value is valid
		// (dynamic platforms require non-empty after trim, pool platforms accept any non-empty value)
		return sshSecret, nil
	case "ibmz", "ibmp":
		// IBM platforms: validate using validateIBMHostSecret
		if err := validateIBMHostSecret(platform, sshSecret); err != nil {
			return "", fmt.Errorf("%s '%s': invalid ssh-secret '%s': %w", platformType, platform, sshSecret, err)
		}
		return sshSecret, nil
	default:
		return "", fmt.Errorf("invalid type: expect 'aws', 'ibmz', or 'ibmp', got '%s'", cloudProviderType)
	}
}

// parseDynamicPlatformConfig parses and validates a single dynamic platform configuration
// This function extracts configuration for a dynamic platform from the ConfigMap data,
// validates all required and optional fields, and returns a structured DynamicPlatformConfig.
// Dynamic platforms support on-demand cloud instances (AWS EC2, IBM Cloud PowerPC and s390x) for now.
//
// Configuration format in ConfigMap and its validation rules:
// - dynamic.<platform-config-name>.type (required): Cloud provider type - must be "aws" or "ibm" for now
// - dynamic.<platform-config-name>.max-instances (required): Maximum number of instances - must be >= 1 (no upper limit)
// - dynamic.<platform-config-name>.instance-tag (optional): Instance tag for cost control must pass validateDynamicInstanceTag if provided
// - dynamic.<platform-config-name>.allocation-timeout (optional): Timeout in seconds - must be >= 1 (no upper limit, defaults to 600)
// - dynamic.<platform-config-name>.ssh-secret (required): non-empty SSH secret name (AWS platforms) or pass validateIBMHostSecret (IBM platforms)
// - dynamic.<platform-config-name>.sudo-commands (optional): Sudo commands to execute
//
// Parameters:
// - data: The ConfigMap data map containing platform configuration
// - platform: The platform name (e.g., "linux/amd64", "linux/s390x" etc.)
//
// Returns:
// - DynamicPlatformConfig: The parsed and validated configuration
// - error: Validation error if any required field is missing or invalid
func ParseDynamicPlatformConfig(data map[string]string, platform string) (DynamicPlatformConfig, error) {
	platformConfigName := strings.ReplaceAll(platform, "/", "-")
	prefix := "dynamic." + platformConfigName + "."

	dynamicConfig := DynamicPlatformConfig{}

	// Type field (required)
	typeName, err := parseDynamicRequiredTypeField(data, prefix, platform, "dynamic platform")
	if err != nil {
		return DynamicPlatformConfig{}, err
	}
	dynamicConfig.Type = typeName

	// Max instances (required)
	maxInstances, err := parseDynamicRequiredMaxInstancesField(data, prefix, platform, "dynamic platform")
	if err != nil {
		return DynamicPlatformConfig{}, err
	}
	dynamicConfig.MaxInstances = maxInstances

	// Instance tag (optional)
	instanceTag, err := parseDynamicOptionalInstanceTagField(data, prefix, platformConfigName, platform, "dynamic platform")
	if err != nil {
		return DynamicPlatformConfig{}, err
	}
	dynamicConfig.InstanceTag = instanceTag

	// Allocation timeout (optional)
	if timeoutStr := data[prefix+"allocation-timeout"]; timeoutStr != "" {
		timeout, err := validateNonZeroPositiveNumber(timeoutStr)
		if err != nil {
			return DynamicPlatformConfig{}, fmt.Errorf("dynamic platform '%s': invalid allocation-timeout '%s': %w", platform, timeoutStr, err)
		}
		dynamicConfig.AllocationTimeout = int64(timeout)
	} else {
		// Default to 10 minutes if not specified
		dynamicConfig.AllocationTimeout = int64(defaultAllocationTimeout)
	}

	// Check interval
	if intervalStr := data[prefix+"check-interval"]; intervalStr != "" {
		interval, err := validateNonZeroPositiveNumber(intervalStr)
		if err != nil {
			return DynamicPlatformConfig{}, fmt.Errorf("dynamic platform '%s': invalid check-interval '%s': %w", platform, intervalStr, err)
		}
		dynamicConfig.CheckInterval = int32(interval)
	} else {
		// Default to 60 sec if not specified
		dynamicConfig.CheckInterval = int32(defaultCheckInterval)
	}

	// SSH secret (required)
	sshSecret, err := parseRequiredSSHSecretField(data, prefix, platform, "dynamic platform", dynamicConfig.Type)
	if err != nil {
		return DynamicPlatformConfig{}, err
	}
	dynamicConfig.SSHSecret = sshSecret

	// Sudo commands (optional)
	if sudoCommands := data[prefix+"sudo-commands"]; sudoCommands != "" {
		dynamicConfig.SudoCommands = sudoCommands
	}

	return dynamicConfig, nil
}

// parseDynamicPoolPlatformConfig parses and validates a single dynamic pool platform configuration
// This function extracts configuration for a dynamic pool platform from the ConfigMap data,
// validates all required and optional fields, and returns a structured DynamicPoolPlatformConfig.
// Dynamic pool platforms combine fixed and dynamic allocation strategies with auto-scaling and TTL-based lifecycle.
//
// Configuration format in ConfigMap and its validation rules:
// - dynamic.<platform-config-name>.type (required): Cloud provider type - must be "aws" or "ibm" for now
// - dynamic.<platform-config-name>.max-instances (required): Maximum number of instances - must be >= 1 (no upper limit)
// - dynamic.<platform-config-name>.concurrency (required): Concurrent jobs per host - must be between 1 and 8
// - dynamic.<platform-config-name>.max-age (required): Host maximum age in minutes (1-1440)
// - dynamic.<platform-config-name>.instance-tag (optional): Instance tag for cost control must pass validateDynamicInstanceTag if provided
// - dynamic.<platform-config-name>.ssh-secret (required): non-empty SSH secret name (AWS platforms) or pass validateIBMHostSecret (IBM platforms)
//
// Parameters:
// - data: The ConfigMap data map containing platform configuration
// - platform: The platform name (e.g., "linux/amd64", "linux-m2xlarge/amd64")
//
// Returns:
// - DynamicPoolPlatformConfig: The parsed and validated configuration
// - error: Validation error if any required field is missing or invalid
func ParseDynamicPoolPlatformConfig(data map[string]string, platform string) (DynamicPoolPlatformConfig, error) {
	platformConfigName := strings.ReplaceAll(platform, "/", "-")
	prefix := "dynamic." + platformConfigName + "."

	poolConfig := DynamicPoolPlatformConfig{}

	// Type field (required)
	typeName, err := parseDynamicRequiredTypeField(data, prefix, platform, "dynamic pool platform")
	if err != nil {
		return DynamicPoolPlatformConfig{}, err
	}
	poolConfig.Type = typeName

	// Max instances (required)
	maxInstances, err := parseDynamicRequiredMaxInstancesField(data, prefix, platform, "dynamic pool platform")
	if err != nil {
		return DynamicPoolPlatformConfig{}, err
	}
	poolConfig.MaxInstances = maxInstances

	// Concurrency (required)
	if concurrencyStr := data[prefix+"concurrency"]; concurrencyStr != "" {
		concurrency, err := validateNonZeroPositiveNumberWithMax(concurrencyStr, maxStaticConcurrency)
		if err != nil {
			return DynamicPoolPlatformConfig{}, fmt.Errorf("dynamic pool platform '%s': invalid concurrency '%s': %w", platform, concurrencyStr, err)
		}
		poolConfig.Concurrency = concurrency
	} else {
		return DynamicPoolPlatformConfig{}, fmt.Errorf("dynamic pool platform '%s': concurrency field is required", platform)
	}

	// Max age (required)
	if maxAgeStr := data[prefix+"max-age"]; maxAgeStr != "" {
		maxAge, err := validateNonZeroPositiveNumberWithMax(maxAgeStr, maxPoolHostAge)
		if err != nil {
			return DynamicPoolPlatformConfig{}, fmt.Errorf("dynamic pool platform '%s': invalid max-age '%s': %w", platform, maxAgeStr, err)
		}
		poolConfig.MaxAge = int64(maxAge)
	} else {
		return DynamicPoolPlatformConfig{}, fmt.Errorf("dynamic pool platform '%s': max-age field is required", platform)
	}

	// Instance tag (optional)
	instanceTag, err := parseDynamicOptionalInstanceTagField(data, prefix, platformConfigName, platform, "dynamic pool platform")
	if err != nil {
		return DynamicPoolPlatformConfig{}, err
	}
	poolConfig.InstanceTag = instanceTag

	// SSH secret (required)
	sshSecret, err := parseRequiredSSHSecretField(data, prefix, platform, "dynamic pool platform", poolConfig.Type)
	if err != nil {
		return DynamicPoolPlatformConfig{}, err
	}
	poolConfig.SSHSecret = sshSecret

	return poolConfig, nil
}

// parseStaticHostConfig parses and validates a single static host configuration
// This function extracts configuration for a static host from the ConfigMap data,
// validates all required and optional fields, and returns a structured StaticHostConfig.
// Static hosts are fixed, pre-configured hosts with concurrency limits and load balancing.
//
// Configuration format in ConfigMap and its validation rules:
// - host.<hostname>.address (required): IP address must be a valid and reachable IPv4 address
// - host.<hostname>.user (required): SSH user for the host - must not be empty or whitespace-only if provided
// - host.<hostname>.platform (required): Platform identifier (e.g., "linux/s390x") - must pass validatePlatformFormat if provided
// - host.<hostname>.secret (required): non-empty SSH secret name (AWS platforms) or pass validateIBMHostSecret (IBM platforms)
// - host.<hostname>.concurrency (optional): Maximum concurrent jobs - must be between 1 and 8 if provided
//
// Parameters:
// - data: The ConfigMap data map containing host configuration
// - hostName: The hostname identifier used in the ConfigMap keys
//
// Returns:
// - StaticHostConfig: The parsed and validated configuration
// - error: Validation error if address is missing or any field is invalid
func ParseStaticHostConfig(data map[string]string, hostName string) (StaticHostConfig, error) {
	hostConfig := StaticHostConfig{}
	prefix := "host." + hostName + "."

	// Parse and validate each field
	if address := strings.TrimSpace(data[prefix+"address"]); address != "" {
		if err := ValidateIPFormat(address); err != nil {
			return StaticHostConfig{}, fmt.Errorf("static host '%s': invalid address '%s': %w", hostName, address, err)
		}
		hostConfig.Address = address
	}

	if user := strings.TrimSpace(data[prefix+"user"]); user != "" {
		hostConfig.User = user
	}

	if platform := strings.TrimSpace(data[prefix+"platform"]); platform != "" {
		if err := validatePlatformFormat(platform); err != nil {
			return StaticHostConfig{}, fmt.Errorf("static host '%s': invalid platform '%s': %w", hostName, platform, err)
		}
		hostConfig.Platform = platform
	}

	if secret := strings.TrimSpace(data[prefix+"secret"]); secret != "" {
		// For IBM platforms, validate using validateIBMHostSecret
		if strings.Contains(hostConfig.Platform, "s390x") || strings.Contains(hostConfig.Platform, "ppc64le") {
			if err := validateIBMHostSecret(hostConfig.Platform, secret); err != nil {
				return StaticHostConfig{}, fmt.Errorf("static host '%s': invalid secret '%s': %w", hostName, secret, err)
			}
		}
		hostConfig.Secret = secret
	}

	if concurrencyStr := data[prefix+"concurrency"]; concurrencyStr != "" {
		concurrency, err := validateNonZeroPositiveNumberWithMax(concurrencyStr, maxStaticConcurrency)
		if err != nil {
			return StaticHostConfig{}, fmt.Errorf("static host '%s': invalid concurrency '%s': %w", hostName, concurrencyStr, err)
		}
		hostConfig.Concurrency = concurrency
	} else {
		hostConfig.Concurrency = defaultStaticHostsConcurrency
	}

	// Validate that address field was provided
	if hostConfig.Address == "" {
		return StaticHostConfig{}, fmt.Errorf("static host '%s': address field is required", hostName)
	}

	return hostConfig, nil
}

// DynamicPlatformConfig holds configuration for a single dynamic platform
type DynamicPlatformConfig struct {
	Type              string `mapstructure:"type"`
	MaxInstances      int    `mapstructure:"max-instances"`
	InstanceTag       string `mapstructure:"instance-tag,omitempty"`
	AllocationTimeout int64  `mapstructure:"allocation-timeout,omitempty"`
	CheckInterval     int32  `mapstructure:"check-interval,omitempty"` // seconds between IP checks
	SSHSecret         string `mapstructure:"ssh-secret"`
	SudoCommands      string `mapstructure:"sudo-commands,omitempty"`
}

// DynamicPoolPlatformConfig holds configuration for a single dynamic platform in a host pool
type DynamicPoolPlatformConfig struct {
	Type         string `mapstructure:"type"`
	MaxInstances int    `mapstructure:"max-instances"`
	Concurrency  int    `mapstructure:"concurrency"`
	MaxAge       int64  `mapstructure:"max-age"` // in minutes
	InstanceTag  string `mapstructure:"instance-tag,omitempty"`
	SSHSecret    string `mapstructure:"ssh-secret"`
}

// StaticHostConfig represents a single static host configuration
type StaticHostConfig struct {
	Address     string `mapstructure:"address"`
	User        string `mapstructure:"user"`
	Platform    string `mapstructure:"platform"`
	Secret      string `mapstructure:"secret"`
	Concurrency int    `mapstructure:"concurrency"`
}
