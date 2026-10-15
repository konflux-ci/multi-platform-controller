---
name: developer-guide
description: >
  Use when an agent needs to understand multi-platform-controller architecture, identify binaries or entry points,
  locate constants, describe allocation strategies, trace the reconciliation flow, or orient
  itself in the multi-platform-controller codebase.
---

# Developer Guide

Architecture and orientation for the multi-platform-controller codebase.
For the repo description and commands, see `AGENTS.md` in the repo root.

## Three Binaries

| Binary | Entry Point | Purpose |
|--------|------------|---------|
| Controller | `cmd/controller/main.go` | Main reconciler — watches TaskRuns, allocates hosts, manages secrets |
| OTP Server | `cmd/otp/` | In-memory one-time-password to SSH key mapping. **Stateless on restart — all data is lost** |
| Dev Setup | `cmd/devsetup/` | Development environment setup tool |

## Key Entry Points

- **Controller bootstrap:** `pkg/controller/controller.go` — registers the reconciler and starts the manager
- **TaskRun reconciler:** `pkg/reconciler/taskrun/taskrun.go` — core reconciliation logic, TaskRun detection, platform resolution
- **Cloud provider interface:** `pkg/cloud/cloud.go` — implement this to add a new provider
- **AWS EC2 provider:** `pkg/aws/ec2.go` — EC2 instance lifecycle (arm64 and amd64)
- **IBM Power (ppc64le):** `pkg/ibm/ibmp.go` — IBM Power Virtual Server instances
- **IBM Z (s390x):** `pkg/ibm/ibmz.go` — IBM Z virtual server instances
- **Config management:** `pkg/config/config.go` — parses ConfigMap `data` fields into platform configs
- **Prometheus metrics:** `pkg/metrics/` — platform availability and allocation rate metrics
- **Provisioning scripts:** `deploy/operator/provision-shared-host.*` — SSH user creation, key generation, OTP handoff

## Allocation Strategies

### Local — `pkg/reconciler/taskrun/local.go`
Runs the build locally inside the ROSA cluster where the Konflux cluster lives.
Used when the target platform matches the cluster platform (`linux/x86_64`, `local` or `localhost`).
Sets `AssignedHost` to `localhost` and creates a secret with just the host field.

### Static Host Pool — `pkg/reconciler/taskrun/hostpool.go`
Pre-provisioned static hosts with concurrency limits and load balancing.
Selects the host with the most free slots, tracks failed hosts per TaskRun,
and launches a provisioning TaskRun to set up SSH access.

### Dynamic Allocation — `pkg/reconciler/taskrun/dynamic.go`
On-demand cloud instances (AWS EC2, IBM Power, IBM Z).
Instances are created per-request and cleaned up after the TaskRun completes.

### Dynamic Pools — `pkg/reconciler/taskrun/dynamicpool.go`
Hybrid auto-scaling pools with TTL-based lifecycle management.
Instances persist across requests until their TTL expires, balancing cost and latency.

## Reconciliation Flow

1. **TaskRun detection** — watches for TaskRuns that mount a `multi-platform-ssh-*` secret and have a `PLATFORM` param in `Spec.Params` 
2. **Host allocation** — dispatches to the configured strategy (local / static host pool / dynamic / dynamic pool)
3. **Provisioning** — a `provision-shared-host` TaskRun is created that SSHes into the allocated host
   using the ConfigMap user, creates a new user and SSH key pair via `provision-shared-host.sh`,
   then hands the username + SSH key to the OTP server
4. **Secret creation** — the provisioning script creates a Kubernetes Secret with the OTP token
   (or raw SSH key if TLS is not configured) and host details, which the original TaskRun mounts
5. **Cleanup** — finalizers ensure host and secret cleanup on TaskRun completion

## Constants — Split Across Two Files

### In `pkg/reconciler/taskrun/taskrun.go`

```go
SecretPrefix             = "multi-platform-ssh-"
ConfigMapLabel           = "build.appstudio.redhat.com/multi-platform-config"
MultiPlatformSecretLabel = "build.appstudio.redhat.com/multi-platform-secret"
FailedHosts              = "build.appstudio.redhat.com/failed-hosts"
CloudInstanceId          = "build.appstudio.redhat.com/cloud-instance-id"
```

### In `pkg/constant/constant.go`

```go
AssignedHost            = "build.appstudio.redhat.com/assigned-host"
TargetPlatformLabel     = "build.appstudio.redhat.com/target-platform"
WaitingForPlatformLabel = "build.appstudio.redhat.com/waiting-for-platform"
```

## Configuration

- **ConfigMaps** are identified by the label `build.appstudio.redhat.com/multi-platform-config`
- Platform configs use flat key-value pairs in ConfigMap `data` with dot-separated keys
  (e.g., `dynamic.linux-amd64.type`, `dynamic.linux-amd64.max-instances`)
- Configuration loading and parsing lives in `pkg/config/config.go`
- Prometheus metrics for platform availability and allocation rates are exported from `pkg/metrics/`

### SSM Parameter Resolution for AMI Config

AWS dynamic platform configs support the `resolve:ssm:` prefix in the
`ami` field to delegate AMI selection to AWS Systems Manager Parameter
Store. Instead of hardcoding a static AMI ID (e.g., `ami-0b3cc04e917ae5753`),
the config can reference a public or private SSM parameter that always
resolves to the latest AMI for a given OS:

```yaml
dynamic.windows-c4xlarge-amd64.ami: resolve:ssm:/aws/service/ami-windows-latest/Windows_Server-2025-English-Core-Base
```

**How it works:** The `resolve:ssm:` value is passed directly to the EC2
`RunInstances` API via the `ImageId` field (`pkg/aws/ec2_helpers.go`).
EC2 natively resolves SSM parameter paths at launch time — no
controller-side resolution logic is needed. See the [AWS documentation
on finding AMIs with SSM parameters](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/finding-an-ami-parameter-store.html).

**IAM permission requirement:** Any IAM role that launches instances
with an SSM-based AMI config must have `ssm:GetParameters` permission
on the target SSM parameter ARN. This includes:

- The controller's production IAM role
- CI IAM roles used by E2E tests (e.g., `multi-platform-github-action`)
- Any dev/staging roles used for manual testing

Without this permission, `RunInstances` fails with an authorization
error. When reviewing PRs that introduce or modify `resolve:ssm:`
values in deploy overlay configs, verify that the relevant IAM
policies include `ssm:GetParameters` before merging.

**Valid AMI config formats:**

| Format | Example | Notes |
|--------|---------|-------|
| Static AMI ID | `ami-06f37afe6d4f43c47` | Traditional format, must match `ami-[0-9a-f]+` |
| SSM parameter path | `resolve:ssm:/aws/service/ami-windows-latest/...` | Resolved by EC2 at launch time |

**Note on AMI validation (#1098):** Issue #1098 (open as of this writing)
proposes adding AMI ID format validation. Any such validation should
accept the `resolve:ssm:` prefix as a valid format — it is an
intentional feature, not malformed input.

## Troubleshooting

| Symptom | Likely Cause | Where to Look |
|---------|-------------|---------------|
| SSH auth fails after OTP restart | OTP server is in-memory; restart loses all mappings | `cmd/otp/` — check pod restarts |
| TaskRun stuck waiting for platform | No host available or allocation failed | `pkg/reconciler/taskrun/taskrun.go` — check reconcile logs |
| Secret not created | Platform label missing or config not found | Check `ConfigMapLabel` and `TargetPlatformLabel` annotations |
| Cloud instance not cleaned up | Finalizer may have been removed or cleanup errored | `pkg/reconciler/taskrun/dynamic.go` — check cleanup path |
| Metrics not reporting | Metrics endpoint not registered | `pkg/metrics/` — verify metric registration |

## Code Standards

For Go coding standards used in this repo, see `skills/go-coding-standards.md`.
