package taskrun

import (
	"context"
	"fmt"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	// Default allocation timeout for dynamic platforms in seconds (10 minutes)
	defaultAllocationTimeout = 600
)

// parsePlatformList parses a comma-separated list of platforms, validates each one, and returns a slice of valid
// platforms. Handles trailing commas.
func parsePlatformList(platformList string, platformType string) ([]string, error) {
	if platformList == "" {
		return []string{}, nil
	}

	platforms := strings.Split(platformList, ",")
	result := []string{}
	for i, platform := range platforms {
		platform = strings.TrimSpace(platform)
		if platform == "" {
			// Allow empty strings only at the end (from trailing commas)
			if i == len(platforms)-1 {
				continue
			}
			return nil, fmt.Errorf("invalid %s platform '': empty platform in middle of list", platformType)
		}
		if err := validatePlatformFormat(platform); err != nil {
			return nil, fmt.Errorf("invalid %s platform '%s': %w", platformType, platform, err)
		}
		result = append(result, platform)
	}
	return result, nil
}

// ClusterHostConfig represents the complete host configuration from ConfigMap
type ClusterHostConfig struct {
	LocalPlatforms         []string
	DynamicPlatforms       map[string]DynamicPlatformConfig
	DynamicPoolPlatforms   map[string]DynamicPoolPlatformConfig
	StaticPlatforms        map[string]StaticHostConfig
	DefaultInstanceTag     string
	AdditionalInstanceTags map[string]string
}

// DynamicPlatformConfig holds configuration for a single dynamic platform
type DynamicPlatformConfig struct {
	Type              string `mapstructure:"type"`
	MaxInstances      int    `mapstructure:"max-instances"`
	InstanceTag       string `mapstructure:"instance-tag,omitempty"`
	AllocationTimeout int64  `mapstructure:"allocation-timeout,omitempty"`
	SSHSecret         string `mapstructure:"ssh-secret"`
	SudoCommands      string `mapstructure:"sudo-commands,omitempty"`
}

// DynamicPoolPlatformConfig holds configuration for a single dynamic platform in a host pool
type DynamicPoolPlatformConfig struct {
	Type         string `mapstructure:"type"`
	MaxInstances int    `mapstructure:"max-instances"`
	Concurrency  int    `mapstructure:"concurrency"`
	MaxAge       int    `mapstructure:"max-age"` // in minutes
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

// ParseAndValidate parses and validates host configuration data from a ConfigMap
// This function extracts configuration for all platform types (local, dynamic, dynamic-pool, static)
// and validates each field according to the controller's validation rules.
//
// Parameters:
// - ctx: Context for AWS resource validation
// - kubeClient: Kubernetes client for AWS resource validation
// - data: The ConfigMap data map containing host configuration
// - systemNamespace: System namespace for AWS resource validation
//
// Returns:
// - *ClusterHostConfig: The parsed and validated configuration
// - error: Validation error if any field fails validation
func ParseAndValidate(ctx context.Context, kubeClient client.Client, data map[string]string, systemNamespace string) (*ClusterHostConfig, error) {
	config := &ClusterHostConfig{
		LocalPlatforms:         []string{},
		DynamicPlatforms:       make(map[string]DynamicPlatformConfig),
		DynamicPoolPlatforms:   make(map[string]DynamicPoolPlatformConfig),
		StaticPlatforms:        make(map[string]StaticHostConfig),
		AdditionalInstanceTags: make(map[string]string),
	}

	// Parse local platforms
	localPlatforms, err := parsePlatformList(data[LocalPlatforms], "local")
	if err != nil {
		return nil, err
	}
	config.LocalPlatforms = localPlatforms

	// Parse dynamic platforms
	dynamicPlatforms, err := parsePlatformList(data[DynamicPlatforms], "dynamic")
	if err != nil {
		return nil, err
	}
	for _, platform := range dynamicPlatforms {
		platformConfigName := strings.ReplaceAll(platform, "/", "-")
		prefix := "dynamic." + platformConfigName + "."

		// Parse and validate dynamic platform configuration
		dynamicConfig := DynamicPlatformConfig{}

		// Type field (required)
		if typeName := data[prefix+"type"]; typeName != "" {
			dynamicConfig.Type = typeName
		} else {
			return nil, fmt.Errorf("dynamic platform '%s': type field is required", platform)
		}

		// Max instances (required)
		if maxInstancesStr := data[prefix+"max-instances"]; maxInstancesStr != "" {
			maxInstances, err := validateMaxInstances(maxInstancesStr)
			if err != nil {
				return nil, fmt.Errorf("dynamic platform '%s': invalid max-instances '%s': %w", platform, maxInstancesStr, err)
			}
			dynamicConfig.MaxInstances = maxInstances
		} else {
			return nil, fmt.Errorf("dynamic platform '%s': max-instances field is required", platform)
		}

		// Instance tag (optional)
		if instanceTag := data[prefix+"instance-tag"]; instanceTag != "" {
			if err := validateDynamicInstanceTag(platformConfigName, instanceTag); err != nil {
				return nil, fmt.Errorf("dynamic platform '%s': invalid instance-tag '%s': %w", platform, instanceTag, err)
			}
			dynamicConfig.InstanceTag = instanceTag
		}

		// Allocation timeout (optional)
		if timeoutStr := data[prefix+"allocation-timeout"]; timeoutStr != "" {
			timeout, err := validateMaxAllocationTimeout(timeoutStr)
			if err != nil {
				return nil, fmt.Errorf("dynamic platform '%s': invalid allocation-timeout '%s': %w", platform, timeoutStr, err)
			}
			dynamicConfig.AllocationTimeout = int64(timeout)
		} else {
			// Default to 10 minutes if not specified
			dynamicConfig.AllocationTimeout = int64(defaultAllocationTimeout)
		}

		// SSH secret (required)
		if sshSecret := data[prefix+"ssh-secret"]; sshSecret != "" {
			// For AWS platforms, just validate that there is a value
			if dynamicConfig.Type == "aws" {
				// AWS platforms: ensure the field has a non-empty value
				if strings.TrimSpace(sshSecret) == "" {
					return nil, fmt.Errorf("dynamic platform '%s': ssh-secret cannot be empty for AWS platforms", platform)
				}
				dynamicConfig.SSHSecret = sshSecret
			} else {
				// IBM platforms: validate using validateIBMHostSecret
				if err := validateIBMHostSecret(platform, sshSecret); err != nil {
					return nil, fmt.Errorf("dynamic platform '%s': invalid ssh-secret '%s': %w", platform, sshSecret, err)
				}
				dynamicConfig.SSHSecret = sshSecret
			}
		} else {
			return nil, fmt.Errorf("dynamic platform '%s': ssh-secret field is required", platform)
		}

		// Sudo commands (optional)
		if sudoCommands := data[prefix+"sudo-commands"]; sudoCommands != "" {
			dynamicConfig.SudoCommands = sudoCommands
		}

		// For AWS platforms, validate AWS resources
		// TODO: Re-enable this at the start of KFLUXINFRA-2328
		// if dynamicConfig.Type == "aws" {
		// 	awsConfig := AWSResourceValidationConfig{
		// 		Region:          data[prefix+"region"],
		// 		AMI:             data[prefix+"ami"],
		// 		SecurityGroupId: data[prefix+"security-group-id"],
		// 		SubnetId:        data[prefix+"subnet-id"],
		// 		KeyName:         data[prefix+"key-name"],
		// 		AWSSecret:       dynamicConfig.SSHSecret,
		// 		SystemNamespace: systemNamespace,
		// 	}
		// 	if err := validateAWSResources(ctx, kubeClient, awsConfig); err != nil {
		// 		return nil, fmt.Errorf("dynamic platform '%s': AWS resource validation failed: %w", platform, err)
		// 	}
		// }

		config.DynamicPlatforms[platform] = dynamicConfig
	}

	// Parse dynamic pool platforms
	dynamicPoolPlatforms, err := parsePlatformList(data[DynamicPoolPlatforms], "dynamic pool")
	if err != nil {
		return nil, err
	}
	for _, platform := range dynamicPoolPlatforms {
		platformConfigName := strings.ReplaceAll(platform, "/", "-")
		prefix := "dynamic." + platformConfigName + "."

		// Parse and validate dynamic pool platform configuration
		poolConfig := DynamicPoolPlatformConfig{}

		// Type field (required)
		if typeName := data[prefix+"type"]; typeName != "" {
			poolConfig.Type = typeName
		} else {
			return nil, fmt.Errorf("dynamic pool platform '%s': type field is required", platform)
		}

		// Max instances (required)
		if maxInstancesStr := data[prefix+"max-instances"]; maxInstancesStr != "" {
			maxInstances, err := validateMaxInstances(maxInstancesStr)
			if err != nil {
				return nil, fmt.Errorf("dynamic pool platform '%s': invalid max-instances '%s': %w", platform, maxInstancesStr, err)
			}
			poolConfig.MaxInstances = maxInstances
		} else {
			return nil, fmt.Errorf("dynamic pool platform '%s': max-instances field is required", platform)
		}

		// Concurrency (required)
		if concurrencyStr := data[prefix+"concurrency"]; concurrencyStr != "" {
			concurrency, err := validateNumericValue(concurrencyStr, maxStaticConcurrency)
			if err != nil {
				return nil, fmt.Errorf("dynamic pool platform '%s': invalid concurrency '%s': %w", platform, concurrencyStr, err)
			}
			poolConfig.Concurrency = concurrency
		} else {
			return nil, fmt.Errorf("dynamic pool platform '%s': concurrency field is required", platform)
		}

		// Max age (required)
		if maxAgeStr := data[prefix+"max-age"]; maxAgeStr != "" {
			maxAge, err := validateNumericValue(maxAgeStr, maxPoolHostAge)
			if err != nil {
				return nil, fmt.Errorf("dynamic pool platform '%s': invalid max-age '%s': %w", platform, maxAgeStr, err)
			}
			poolConfig.MaxAge = maxAge
		} else {
			return nil, fmt.Errorf("dynamic pool platform '%s': max-age field is required", platform)
		}

		// Instance tag (optional)
		if instanceTag := data[prefix+"instance-tag"]; instanceTag != "" {
			if err := validateDynamicInstanceTag(platformConfigName, instanceTag); err != nil {
				return nil, fmt.Errorf("dynamic pool platform '%s': invalid instance-tag '%s': %w", platform, instanceTag, err)
			}
			poolConfig.InstanceTag = instanceTag
		}

		// SSH secret (required)
		if sshSecret := data[prefix+"ssh-secret"]; sshSecret != "" {
			// For IBM platforms (non-AWS), validate using validateIBMHostSecret
			if poolConfig.Type != "aws" {
				if err := validateIBMHostSecret(platform, sshSecret); err != nil {
					return nil, fmt.Errorf("dynamic pool platform '%s': invalid ssh-secret '%s': %w", platform, sshSecret, err)
				}
			}
			poolConfig.SSHSecret = sshSecret
		} else {
			return nil, fmt.Errorf("dynamic pool platform '%s': ssh-secret field is required", platform)
		}

		// For AWS platforms, validate AWS resources
		// TODO: Re-enable this with proper mocking in tests at the start of KFLUXINFRA-2328
		// if poolConfig.Type == "aws" {
		// 	awsConfig := AWSResourceValidationConfig{
		// 		Region:          data[prefix+"region"],
		// 		AMI:             data[prefix+"ami"],
		// 		SecurityGroupId: data[prefix+"security-group-id"],
		// 		SubnetId:        data[prefix+"subnet-id"],
		// 		KeyName:         data[prefix+"key-name"],
		// 		AWSSecret:       poolConfig.SSHSecret,
		// 		SystemNamespace: systemNamespace,
		// 	}
		// 	if err := validateAWSResources(ctx, kubeClient, awsConfig); err != nil {
		// 		return nil, fmt.Errorf("dynamic pool platform '%s': AWS resource validation failed: %w", platform, err)
		// 	}
		// }

		config.DynamicPoolPlatforms[platform] = poolConfig
	}

	// Parse static hosts (host.<name>.* pattern)
	staticHosts := make(map[string]StaticHostConfig)
	for key, value := range data {
		if !strings.HasPrefix(key, "host.") {
			continue
		}

		// Extract host name and field from key
		key = key[len("host."):]
		pos := strings.LastIndex(key, ".")
		if pos == -1 {
			continue
		}

		hostName := key[0:pos]
		field := key[pos+1:]

		// Get or create host config
		hostConfig, exists := staticHosts[hostName]
		if !exists {
			hostConfig = StaticHostConfig{}
		}

		// Parse and validate each field
		switch field {
		case "address":
			if err := validateIP(value); err != nil {
				return nil, fmt.Errorf("static host '%s': invalid address '%s': %w", hostName, value, err)
			}
			hostConfig.Address = value

		case "user":
			if strings.TrimSpace(value) == "" {
				return nil, fmt.Errorf("static host '%s': user field cannot be empty", hostName)
			}
			hostConfig.User = value

		case "platform":
			if err := validatePlatformFormat(value); err != nil {
				return nil, fmt.Errorf("static host '%s': invalid platform '%s': %w", hostName, value, err)
			}
			hostConfig.Platform = value

		case "secret":
			// For IBM platforms, validate using validateIBMHostSecret
			if strings.Contains(hostConfig.Platform, "s390x") || strings.Contains(hostConfig.Platform, "ppc64le") {
				if err := validateIBMHostSecret(hostConfig.Platform, value); err != nil {
					return nil, fmt.Errorf("static host '%s': invalid secret '%s': %w", hostName, value, err)
				}
			}
			hostConfig.Secret = value

		case "concurrency":
			concurrency, err := validateNumericValue(value, maxStaticConcurrency)
			if err != nil {
				return nil, fmt.Errorf("static host '%s': invalid concurrency '%s': %w", hostName, value, err)
			}
			hostConfig.Concurrency = concurrency
		}

		staticHosts[hostName] = hostConfig
	}

	// Add validated static hosts to config, keyed by hostname
	for hostName, hostConfig := range staticHosts {
		// Only validate that address field was provided (others are validated during parsing)
		if hostConfig.Address == "" {
			return nil, fmt.Errorf("static host '%s': address field is required", hostName)
		}

		config.StaticPlatforms[hostName] = hostConfig
	}

	// Parse default instance tag
	if instanceTag := data[DefaultInstanceTag]; instanceTag != "" {
		config.DefaultInstanceTag = instanceTag
	}

	// Parse additional instance tags
	if additionalTags := data[AdditionalInstanceTags]; additionalTags != "" {
		tags := strings.Split(additionalTags, ",")
		for _, tag := range tags {
			tag = strings.TrimSpace(tag)
			parts := strings.Split(tag, "=")
			if len(parts) >= 2 {
				config.AdditionalInstanceTags[parts[0]] = parts[1]
			} else {
				return nil, fmt.Errorf("invalid additional instance tag format '%s': must be key=value", tag)
			}
		}
	}

	return config, nil
}
