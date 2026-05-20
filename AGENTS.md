# Multi-Platform-Controller

Kubernetes controller that allocates multi-architecture build hosts for Konflux.
Watches TaskRuns, provisions hosts via AWS/IBM Cloud, and manages SSH credentials.

## Quick Commands

| Action | Command |
|--------|---------|
| Build controller | `make build` |
| Build OTP server | `make build-otp` |
| Run tests | `make test` (runs fmt and vet first) |
| Lint | `make lint` |
| Format | `make fmt` (included in make test) |
| Vet | `make vet` (included in make test) |
| Generate RBAC manifests | `make manifests` |
| Run e2e tests | `make test-e2e` (CI only — runs deployment, taskrun, otelcol suites) |

## Project Layout

- `cmd/controller/`, `cmd/otp/`, `cmd/devsetup/` — three binaries (controller, OTP server, dev setup)
- `pkg/reconciler/taskrun/` — reconciliation logic and allocation strategies (local, static host pool, dynamic, dynamic pool)
- `pkg/aws/`, `pkg/ibm/` — cloud provider implementations
- `pkg/cloud/` — cloud provider interface
- `pkg/config/` — ConfigMap-based configuration loading and parsing
- `pkg/metrics/` — Prometheus metrics
- `deploy/operator/` — provisioning scripts and task definitions
- `skills/` — deep-dive reference files for agents

## Key Conventions

- **Pre-push:** run `make fmt`, `make lint`, `make test` — iterate until all pass before pushing
- **Coverage:** tracked by codecov. Aim for 100% patch coverage; never drop repository below 76%
- **Large PRs (>8 files):** split into smaller chunks with tests, or pair a code PR with a test PR
  and link both in descriptions
- **Commits:** Jira ID at start (e.g., `KFLUXINFRA-1234 description`). Interactive sessions:
  `Co-Authored-By:` trailer. Agentic workflow: `Authored-By:` trailer. Include agent name and tool.
- **Review:** anyone can review; approvals from konflux-ci org infrastructure team, 1 required, 2 preferred
- **Agentic PRs:** open as draft → monitor CI with `gh pr checks` → iterate → `gh pr ready`
  when all checks pass
- **Post-merge deployment:** staging → private prod → 24h soak → public prod, all via
  `redhat-appstudio/infra-deployments` GitHub repo (GitOps, ArgoCD).
  See `skills/pr-workflow.md` for the full deployment process.

## Gotchas

- **OTP server is stateless on restart** — all in-memory password-to-key mappings are lost,
  causing in-flight SSH handshakes to fail. Check pod restarts when debugging auth failures.
- **E2E tests skip silently for unrecognized authors** — e2e only runs if the PR author is
  OWNER/MEMBER/COLLABORATOR in konflux-ci, or the branch is on the repo (not a fork). Agents
  operating from forks with unrecognized identities will never see e2e results on their PRs.
  If e2e was skipped on the PR but fails after merge, fix immediately — main must stay green.
- **Constants are split across two files** — `pkg/reconciler/taskrun/taskrun.go` has `SecretPrefix`,
  `ConfigMapLabel`, `MultiPlatformSecretLabel`, `FailedHosts`, `CloudInstanceId`;
  `pkg/constant/constant.go` has `AssignedHost`, `TargetPlatformLabel`, `WaitingForPlatformLabel`.

## Skills Reference

For deeper guidance on specific topics, see:

- Architecture, allocation strategies, reconciliation flow: `skills/developer-guide.md`
- Go coding standards: `skills/go-coding-standards.md`
- Ginkgo/Gomega test conventions: `skills/testing-standards.md`
- CI workflows, PR lifecycle, agentic workflow details: `skills/pr-workflow.md`
- Receiving and responding to code review: `skills/receiving-code-review.md`
- Interactive design workflow (human-paired sessions): `skills/brainstorming-workflow.md`
