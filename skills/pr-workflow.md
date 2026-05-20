---
name: pr-workflow
description: >
  Use when an agent is opening, monitoring, or iterating on a pull request
  in the multi-platform-controller repository, including CI interpretation,
  coverage requirements, commit conventions, and post-merge deployment.
---

# PR Workflow

Full lifecycle reference for pull requests in the multi-platform-controller repository.

## Pre-Push Verification

Run locally and iterate until everything passes before pushing:

```bash
make fmt          # format code
make lint         # golangci-lint
make test         # unit tests (Ginkgo, race, coverage)
```

## CI Workflows

| Workflow | File | What it checks |
|----------|------|----------------|
| **Validate PR - golang CI** | `go-ci.yaml` | golangci-lint, `go mod tidy` cleanliness, format check |
| **Validate PR - Testing Phase** | `mpc-test.yaml` | `make build` + `make test` with coverage upload to Codecov |
| **E2E Tests** | `test-e2e.yml` | End-to-end tests on a Kind cluster with AWS resources |

All three trigger on pull requests targeting `main` (including drafts) and on merge-group checks.

## E2E Test Details

Three sequential suites run inside the single `test-e2e.yml` job:

1. `make test-e2e-deployment` -- deployment validation
2. `make test-e2e-taskrun` -- TaskRun execution (parallel)
3. `make test-e2e-otelcol` -- otelcol log verification

**Cannot run locally.** The workflow uses AWS OIDC (`id-token: write`) to assume
an IAM role in `us-east-1` for EC2 and S3 access.

### Author Association Gate

E2E tests **only run** when one of these is true:

- The PR branch lives on the repo itself (not a fork).
- The PR author owns the fork **and** has association `OWNER`, `MEMBER`, or
  `COLLABORATOR` in the `konflux-ci` org.

If neither condition is met, e2e tests **skip silently** -- no failure, no status
check. Agents operating from forks with unrecognized identities will never see
e2e results on their PRs.

## Agentic Workflow (Autonomous, No Human)

1. Make changes; run pre-push verification locally (`make fmt && make lint && make test`).
2. Open a **draft PR** -- all CI workflows trigger on drafts.
3. Monitor CI: `gh pr checks <PR-number> --watch`.
4. If CI fails: read logs (`gh run view <run-id> --log-failed`), fix, push. CI re-runs automatically.
5. **Verify coverage:** check `make test` output for coverage percentage and read Codecov comments
   on the PR. If patch coverage is below 100% or repository coverage drops below 76%, add tests
   and push again.
6. **Address Qodo comments:** read automated Qodo review comments on the PR and fix any issues
   they flag. These are automated code quality checks — address them silently during iteration.
7. Iterate steps 3-6 until all checks pass and coverage requirements are met.
8. Mark ready for review: `gh pr ready <PR-number>`.
9. Human reviewers and external reviewers comment on the PR. Read and address their feedback
   following `skills/receiving-code-review.md` — evaluate suggestions technically before
   implementing, push back with reasoning when appropriate.
10. Iterate until approved and merged.

## E2E After Merge

`mpc-test.yaml` and `test-e2e.yml` also trigger on `push` to `main`. If e2e
was skipped on the PR (author association gate) but fails after merge, fix
immediately -- main must stay green.

## Coverage Requirements

- **Patch coverage target:** 100% of new/changed lines.
- **Repository floor:** 76% overall.
- **Large PRs (>8 files):** split into smaller PRs with their own tests, or pair
  a code-only PR with a dedicated test PR and link both in descriptions. Either
  approach keeps review manageable while maintaining coverage.

## Commit Messages

Format: `KFLUXINFRA-<number> <description>`

```
KFLUXINFRA-1234 Add ARM64 dynamic pool support
```

**Interactive sessions (human-paired):** the agent is assisting — use `Co-Authored-By:` trailer
with the agent's name and tool identifier.

**Agentic workflow (autonomous):** the agent is the author — use `Authored-By:` trailer
with the agent's name and tool identifier.

## Review Requirements

- Anyone can review and leave comments.
- **Approvals** must come from members of the **konflux-ci** org, infrastructure team.
- **One approval** required, **two preferred** before merge.
- Complete the PR template fully (summary, test plan, related issues).

## Post-Merge Deployment

Releases go through the `redhat-appstudio/infra-deployments` GitHub repo (GitOps, ArgoCD):

1. **Staging:** approve the automated PR from `rh-tap-build-team` bot — verify the image SHA
   in `components/multi-platform-controller/base/kustomization.yaml` matches the merged commit,
   `/lgtm` and merge.
2. **Private production:** manual PR updating the image SHA in
   `components/multi-platform-controller/production-downstream/base/kustomization.yaml` —
   deploy alongside staging to test under real load (staging has no real traffic).
3. **Soak ~24 hours** monitoring staging and private prod for issues.
4. **Public production:** manual PRs per cluster, updating image SHA in
   `components/multi-platform-controller/production/<cluster-name>/kustomization.yaml`.