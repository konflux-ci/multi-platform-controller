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

## Host Readiness Contract

MPC determines whether a cloud host is ready to accept build work by performing a TCP dial to port 22.
When this dial succeeds, MPC considers the host reachable and launches a provisioning task that SSHes in to create a builder user and configure SSH keys. Build tasks begin only after provisioning completes.

- **AWS:** `pkg/aws/ec2_helpers.go` — `pingIPAddress()` dials `<ip>:22` with a 60-second timeout.
- **IBM Z and IBM Power:** `pkg/ibm/ibmz_helpers.go` — `checkIfIpIsLive()` resolves and dials `<ip>:22` with a 5-second timeout.

### Invariant

All services required by build tasks — Docker engine, builder user creation, SSH key configuration — MUST be fully configured before port 22 becomes reachable from MPC. In user-data scripts, the step that opens port 22 (firewall rule or sshd start) must be the **last** step.

### Why This Matters

If port 22 opens before Docker is installed, MPC will connect and attempt Docker-dependent builds on a host where Docker is not ready. These failures present as transient SSH or build errors that are difficult to diagnose because the host appears reachable and healthy.

### Platform-Specific Notes

- **Windows hosts:** The firewall rule (`New-NetFirewallRule` for port 22) controls reachability. The user-data script in `deploy/overlays/*/host-config.yaml` must place this rule after all provisioning steps (Docker install, user creation, key setup). See the inline comment in the dev-template overlay for the canonical example.
- **Linux hosts:** Port 22 is typically available once sshd starts, so the user-data script ordering and sshd start timing serve the same gating function. Ensure sshd is started or restarted only after all provisioning is complete.

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

## Troubleshooting

| Symptom | Likely Cause | Where to Look |
|---------|-------------|---------------|
| SSH auth fails after OTP restart | OTP server is in-memory; restart loses all mappings | `cmd/otp/` — check pod restarts |
| TaskRun stuck waiting for platform | No host available or allocation failed | `pkg/reconciler/taskrun/taskrun.go` — check reconcile logs |
| Secret not created | Platform label missing or config not found | Check `ConfigMapLabel` and `TargetPlatformLabel` annotations |
| Cloud instance not cleaned up | Finalizer may have been removed or cleanup errored | `pkg/reconciler/taskrun/dynamic.go` — check cleanup path |
| Metrics not reporting | Metrics endpoint not registered | `pkg/metrics/` — verify metric registration |
| Build tasks fail with Docker errors on newly provisioned hosts | User-data script opens SSH port before Docker installation completes | `deploy/overlays/*/host-config.yaml` user-data section — verify firewall rule is after Docker install |

## Code Standards

For Go coding standards used in this repo, see `skills/go-coding-standards.md`.
