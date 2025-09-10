package taskrun

import (
	"strings"

	errors2 "github.com/pkg/errors"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

var (
	errInvalidPlatformFormat    = errors2.New("platform must be in format 'label/label' where each label follows Kubernetes RFC 1035 label name format")
	errMissingPlatformParameter = errors2.New("PLATFORM parameter not found in TaskRun parameters")
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
