/*
Copyright 2025 Red Hat, Inc.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Config struct {
	QuayUsername       string
	InstanceTag        string
	AWSSSHKeyName      string
	IDRSAKeyPath       string
	AWSAccessKeyID     string
	AWSSecretAccessKey string
	AWSSessionToken    string
	IBMCloudAPIKey     string
	GithubRunID        string
	DeployDir          string
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "devsetup",
		Short: "Development setup tool for multi-platform-controller",
	}

	rootCmd.AddCommand(newDeployCmd())
	rootCmd.AddCommand(newCleanupKeypairCmd())
	rootCmd.AddCommand(newCleanupInstancesCmd())

	return rootCmd
}

func newDeployCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "deploy",
		Short: "Deploy the multi-platform-controller for development",
		Long: `Deploy the multi-platform-controller to a Kubernetes cluster for development.

Required environment variables:
  QUAY_USERNAME         - Quay.io username for container images
  AWS_ACCESS_KEY_ID     - AWS access key ID
  AWS_SECRET_ACCESS_KEY - AWS secret access key
  AWS_SESSION_TOKEN     - AWS session token

Optional environment variables:
  ID_RSA_KEY_PATH       - Path to existing SSH key (if not set, creates new EC2 key pair)
  AWS_SSH_KEY_NAME      - Name of existing AWS SSH key (required if ID_RSA_KEY_PATH is set)
  INSTANCE_TAG          - Tag for EC2 instances (default: <username>-development)
  GITHUB_RUN_ID         - GitHub Actions run ID (used for unique naming)
  IBM_CLOUD_API_KEY     - IBM Cloud API key`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeploy()
		},
	}
}

func newCleanupKeypairCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cleanup-keypair <suffix>",
		Short: "Delete an EC2 key pair created during development/testing",
		Long: `Delete an EC2 key pair with name "multi-platform-controller-<suffix>".

This is typically used to clean up key pairs created during e2e tests or development.
The suffix is usually the GITHUB_RUN_ID or a random identifier.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCleanupKeypair(args[0])
		},
	}
}

func newCleanupInstancesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cleanup-instances <instance-tag>",
		Short: "Terminate EC2 instances created during development/testing",
		Long: `Find and terminate EC2 instances tagged with the specified instance tag.

This command finds all EC2 instances with tags:
  - multi-platform-instance=<instance-tag>
  - MultiPlatformManaged=true

And terminates instances in running, pending, stopped, or stopping states.
This is typically used to clean up instances created during e2e tests.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCleanupInstances(args[0])
		},
	}
}

func runDeploy() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	if err := validatePrerequisites(cfg); err != nil {
		return err
	}

	if err := setupNamespace(); err != nil {
		return fmt.Errorf("setting up namespace: %w", err)
	}

	if err := setupSSHKey(cfg); err != nil {
		return fmt.Errorf("setting up SSH key: %w", err)
	}

	if err := createKubernetesSecrets(cfg); err != nil {
		return fmt.Errorf("creating kubernetes secrets: %w", err)
	}

	if err := prepareDeploymentOverlays(cfg); err != nil {
		return fmt.Errorf("preparing deployment overlays: %w", err)
	}

	if err := deployOperator(cfg); err != nil {
		return fmt.Errorf("deploying operator: %w", err)
	}

	return nil
}

func runCleanupKeypair(suffix string) error {
	if err := validateAWSCredentials(); err != nil {
		return err
	}

	keyName := "multi-platform-controller-" + suffix
	fmt.Printf("Deleting AWS EC2 key pair: %s\n", keyName)

	ctx := context.Background()
	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("loading AWS config: %w", err)
	}

	ec2Client := ec2.NewFromConfig(awsCfg)
	_, err = ec2Client.DeleteKeyPair(ctx, &ec2.DeleteKeyPairInput{
		KeyName: aws.String(keyName),
	})
	if err != nil {
		// DeleteKeyPair is idempotent and succeeds even if key doesn't exist,
		// so any error here is a real error we should report
		return fmt.Errorf("deleting key pair: %w", err)
	}

	fmt.Printf("Successfully deleted key pair: %s\n", keyName)
	return nil
}

func runCleanupInstances(instanceTag string) error {
	if err := validateAWSCredentials(); err != nil {
		return err
	}

	fmt.Printf("Finding EC2 instances with tag multi-platform-instance=%s\n", instanceTag)

	ctx := context.Background()
	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("loading AWS config: %w", err)
	}

	ec2Client := ec2.NewFromConfig(awsCfg)

	// Find instances with matching tags
	result, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("tag:multi-platform-instance"),
				Values: []string{instanceTag},
			},
			{
				Name:   aws.String("tag:MultiPlatformManaged"),
				Values: []string{"true"},
			},
			{
				Name:   aws.String("instance-state-name"),
				Values: []string{"running", "pending", "stopped", "stopping"},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("describing instances: %w", err)
	}

	// Collect instance IDs
	var instanceIDs []string
	for _, reservation := range result.Reservations {
		for _, instance := range reservation.Instances {
			if instance.InstanceId != nil {
				instanceIDs = append(instanceIDs, *instance.InstanceId)
			}
		}
	}

	if len(instanceIDs) == 0 {
		fmt.Printf("No EC2 instances found with tag multi-platform-instance=%s\n", instanceTag)
		return nil
	}

	fmt.Printf("Terminating EC2 instances: %v\n", instanceIDs)

	// Terminate each instance
	for _, instanceID := range instanceIDs {
		fmt.Printf("Terminating instance: %s\n", instanceID)
		_, err := ec2Client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
			InstanceIds: []string{instanceID},
		})
		if err != nil {
			fmt.Printf("Failed to terminate instance %s: %v\n", instanceID, err)
			// Continue with other instances
		}
	}

	return nil
}

func loadConfig() (*Config, error) {
	// Always use deploy directory relative to working directory
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting working directory: %w", err)
	}
	deployDir := filepath.Join(wd, "deploy")

	if _, err := os.Stat(deployDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("deploy directory not found: %s", deployDir)
	}

	return &Config{
		QuayUsername:       os.Getenv("QUAY_USERNAME"),
		InstanceTag:        os.Getenv("INSTANCE_TAG"),
		AWSSSHKeyName:      os.Getenv("AWS_SSH_KEY_NAME"),
		IDRSAKeyPath:       os.Getenv("ID_RSA_KEY_PATH"),
		AWSAccessKeyID:     os.Getenv("AWS_ACCESS_KEY_ID"),
		AWSSecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
		AWSSessionToken:    os.Getenv("AWS_SESSION_TOKEN"),
		IBMCloudAPIKey:     os.Getenv("IBM_CLOUD_API_KEY"),
		GithubRunID:        os.Getenv("GITHUB_RUN_ID"),
		DeployDir:          deployDir,
	}, nil
}

func validatePrerequisites(cfg *Config) error {
	if cfg.QuayUsername == "" {
		return errors.New("QUAY_USERNAME is not set")
	}
	return validateAWSCredentials()
}

func validateAWSCredentials() error {
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		return errors.New("AWS_ACCESS_KEY_ID is not set")
	}
	if os.Getenv("AWS_SECRET_ACCESS_KEY") == "" {
		return errors.New("AWS_SECRET_ACCESS_KEY is not set")
	}
	if os.Getenv("AWS_SESSION_TOKEN") == "" {
		return errors.New("AWS_SESSION_TOKEN is not set")
	}
	return nil
}

func setupNamespace() error {
	fmt.Println("Setting up namespace...")

	cmd := exec.Command("kubectl", "create", "namespace", "-o", "yaml", "--dry-run=client", "multi-platform-controller")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("generating namespace yaml: %w", err)
	}

	apply := exec.Command("kubectl", "apply", "-f", "-")
	apply.Stdin = strings.NewReader(string(output))
	apply.Stdout = os.Stdout
	apply.Stderr = os.Stderr
	if err := apply.Run(); err != nil {
		return fmt.Errorf("applying namespace: %w", err)
	}

	cmd = exec.Command("kubectl", "config", "set-context", "--current", "--namespace=multi-platform-controller")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func setupSSHKey(cfg *Config) error {
	if cfg.IDRSAKeyPath != "" {
		fmt.Println("Using existing SSH key path:", cfg.IDRSAKeyPath)
		return nil
	}

	fmt.Println("ID_RSA_KEY_PATH not set, creating new SSH key pair using AWS EC2...")

	ctx := context.Background()
	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("loading AWS config: %w", err)
	}

	// Generate key name
	if cfg.GithubRunID != "" {
		cfg.AWSSSHKeyName = "multi-platform-controller-" + cfg.GithubRunID
	} else {
		randomBytes := make([]byte, 4)
		if _, err := rand.Read(randomBytes); err != nil {
			return fmt.Errorf("generating random bytes: %w", err)
		}
		cfg.AWSSSHKeyName = "multi-platform-controller-" + hex.EncodeToString(randomBytes)
	}
	cfg.IDRSAKeyPath = "/tmp/" + cfg.AWSSSHKeyName + ".pem"

	// Get creator role ARN for tagging
	creatorRoleARN := getCreatorRoleARN(ctx, awsCfg)

	// Create the key pair
	ec2Client := ec2.NewFromConfig(awsCfg)
	input := &ec2.CreateKeyPairInput{
		KeyName: aws.String(cfg.AWSSSHKeyName),
	}

	if creatorRoleARN != "" {
		fmt.Println("Creating key pair with CreatorRole tag:", creatorRoleARN)
		input.TagSpecifications = []types.TagSpecification{
			{
				ResourceType: types.ResourceTypeKeyPair,
				Tags: []types.Tag{
					{
						Key:   aws.String("CreatorRole"),
						Value: aws.String(creatorRoleARN),
					},
				},
			},
		}
	} else {
		fmt.Println("Warning: No role ARN available, creating key pair without CreatorRole tag")
	}

	result, err := ec2Client.CreateKeyPair(ctx, input)
	if err != nil {
		return fmt.Errorf("creating key pair: %w", err)
	}

	if err := os.WriteFile(cfg.IDRSAKeyPath, []byte(*result.KeyMaterial), 0600); err != nil {
		return fmt.Errorf("writing key file: %w", err)
	}

	fmt.Println("Created SSH key pair:", cfg.AWSSSHKeyName)
	return nil
}

func getCreatorRoleARN(ctx context.Context, awsCfg aws.Config) string {
	stsClient := sts.NewFromConfig(awsCfg)
	result, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to get AWS caller identity: %v\n", err)
		return ""
	}

	// Convert assumed-role ARN to IAM role ARN
	// arn:aws:sts::ACCOUNT:assumed-role/ROLE-NAME/SESSION -> arn:aws:iam::ACCOUNT:role/ROLE-NAME
	arn := *result.Arn
	if !strings.Contains(arn, ":assumed-role/") {
		return arn
	}

	arn = strings.Replace(arn, ":sts:", ":iam:", 1)
	parts := strings.Split(arn, "/")
	if len(parts) >= 2 {
		// parts[0] = "arn:aws:iam::ACCOUNT:assumed-role"
		// parts[1] = "ROLE-NAME"
		// parts[2] = "SESSION" (ignored)
		prefix := strings.Replace(parts[0], ":assumed-role", ":role", 1)
		arn = prefix + "/" + parts[1]
	}
	return arn
}

func createKubernetesSecrets(cfg *Config) error {
	fmt.Println("Creating Kubernetes secrets...")

	if cfg.IDRSAKeyPath == "" {
		return errors.New("ID_RSA_KEY_PATH is not set")
	}

	// Delete existing secrets (ignore errors as they may not exist)
	cmd := exec.Command("kubectl", "delete", "--ignore-not-found", "secret", "awskeys", "awsiam", "ibmiam")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()

	// Create awskeys secret
	cmd = exec.Command("kubectl", "create", "secret", "generic", "awskeys", //nolint:gosec // G204 - dev tool with trusted input
		"--from-file=id_rsa="+cfg.IDRSAKeyPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("creating awskeys secret: %w", err)
	}

	// Create awsiam secret using stdin to avoid secrets in command line args
	if err := createSecretFromLiterals("awsiam", map[string]string{
		"access-key-id":     cfg.AWSAccessKeyID,
		"secret-access-key": cfg.AWSSecretAccessKey,
		"session-token":     cfg.AWSSessionToken,
	}); err != nil {
		return fmt.Errorf("creating awsiam secret: %w", err)
	}

	// Create ibmiam secret using stdin to avoid secrets in command line args
	if err := createSecretFromLiterals("ibmiam", map[string]string{
		"api-key": cfg.IBMCloudAPIKey,
	}); err != nil {
		return fmt.Errorf("creating ibmiam secret: %w", err)
	}

	// Label secrets
	for _, secret := range []string{"awsiam", "ibmiam", "awskeys"} {
		cmd = exec.Command("kubectl", "label", "secrets", secret, "build.appstudio.redhat.com/multi-platform-secret=true") //nolint:gosec // G204 - dev tool with trusted input
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("labeling secret %s: %w", secret, err)
		}
	}

	return nil
}

func createSecretFromLiterals(name string, data map[string]string) error {
	// Generate secret JSON directly to avoid passing secrets as command-line arguments
	secretJSON, err := generateSecretJSON(name, data)
	if err != nil {
		return err
	}

	// Apply the secret via stdin
	apply := exec.Command("kubectl", "apply", "-f", "-")
	apply.Stdin = strings.NewReader(secretJSON)
	apply.Stdout = os.Stdout
	apply.Stderr = os.Stderr
	return apply.Run()
}

func generateSecretJSON(name string, data map[string]string) (string, error) {
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Type:       corev1.SecretTypeOpaque,
		StringData: data,
	}

	jsonBytes, err := json.Marshal(secret)
	if err != nil {
		return "", fmt.Errorf("marshaling secret to json: %w", err)
	}
	return string(jsonBytes), nil
}

func prepareDeploymentOverlays(cfg *Config) error {
	fmt.Println("Installing the Operator")

	deployDir := cfg.DeployDir
	developmentDir := filepath.Join(deployDir, "overlays", "development")

	// Remove existing development overlay
	if err := os.RemoveAll(developmentDir); err != nil {
		return fmt.Errorf("removing development overlay: %w", err)
	}

	// Copy dev-template to development
	devTemplateDir := filepath.Join(deployDir, "overlays", "dev-template")
	if err := os.CopyFS(developmentDir, os.DirFS(devTemplateDir)); err != nil {
		return fmt.Errorf("copying dev-template: %w", err)
	}

	// Replace placeholders in development overlay YAML files
	if err := replaceInFiles(developmentDir, "QUAY_USERNAME", cfg.QuayUsername); err != nil {
		return fmt.Errorf("replacing QUAY_USERNAME: %w", err)
	}

	// Replace imagePullPolicy in operator and otp directories
	for _, subdir := range []string{"operator", "otp"} {
		dir := filepath.Join(deployDir, subdir)
		if err := replaceInFiles(dir, "imagePullPolicy: Always", "imagePullPolicy: IfNotPresent"); err != nil {
			return fmt.Errorf("replacing imagePullPolicy in %s: %w", subdir, err)
		}
	}

	// Generate instance tag if not provided
	if cfg.InstanceTag == "" {
		if cfg.GithubRunID != "" {
			cfg.InstanceTag = cfg.GithubRunID + "-development"
		} else {
			cfg.InstanceTag = cfg.QuayUsername + "-development"
		}
	}

	if err := replaceInFiles(developmentDir, "INSTANCE_TAG", cfg.InstanceTag); err != nil {
		return fmt.Errorf("replacing INSTANCE_TAG: %w", err)
	}

	if cfg.AWSSSHKeyName == "" {
		return errors.New("AWS_SSH_KEY_NAME is not set")
	}
	if err := replaceInFiles(developmentDir, "AWS_SSH_KEY_NAME", cfg.AWSSSHKeyName); err != nil {
		return fmt.Errorf("replacing AWS_SSH_KEY_NAME: %w", err)
	}

	return nil
}

func replaceInFiles(dir, old, new string) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return err
		}
		data, err := os.ReadFile(path) //nolint:gosec // G304 - dev tool walking controlled directory
		if err != nil {
			return err
		}
		newData := strings.ReplaceAll(string(data), old, new)
		if newData != string(data) {
			return os.WriteFile(path, []byte(newData), 0600)
		}
		return nil
	})
}

func deployOperator(cfg *Config) error {
	fmt.Println("Deploying operator...")

	developmentDir := filepath.Join(cfg.DeployDir, "overlays", "development")

	cmd := exec.Command("kubectl", "apply", "-k", developmentDir) //nolint:gosec // G204 - dev tool with trusted input
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("applying kustomization: %w", err)
	}

	cmd = exec.Command("kubectl", "rollout", "restart", "deployment", "-n", "multi-platform-controller", "multi-platform-controller")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
