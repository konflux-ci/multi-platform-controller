---
name: ci-workflow-security
description: >
  Use when reviewing or modifying GitHub Actions workflows to check for
  CI/CD security patterns including TOCTOU vulnerabilities, secret exposure,
  workflow command injection, and ref pinning.
---

# CI/CD Workflow Security

Security patterns for GitHub Actions workflows in multi-platform-controller.
Apply when reviewing or writing any `.github/workflows/*.yml` file that handles
pull requests, issue comments, or untrusted inputs.

## 1. TOCTOU in Authorization Flows

**Time-of-Check Time-of-Use (TOCTOU)** is the most critical CI/CD vulnerability
class in workflows that authorize a user and then run code from a PR.

### The pattern

A workflow checks whether a user is authorized (time-of-check), then checks out
or runs code from the PR (time-of-use). Between those two moments, a malicious
push to the PR branch can swap the code being tested.

### What to look for

- **Authorization and checkout in separate steps or jobs.** Any gap between
  "verify the requester is trusted" and "check out the code" is exploitable if
  the checkout uses a mutable ref.
- **Mutable refs after authorization.** `refs/pull/N/head`, branch names, and
  `github.event.pull_request.head.sha` (in `pull_request_target` context) all
  resolve at read time, not at authorization time. A push between the two
  changes what they resolve to.
- **Reusable workflow calls without pinned refs.** When the authorization job
  passes a ref to a `workflow_call` callee, the callee must use that exact ref
  — not re-resolve `refs/pull/N/head`.

### Correct pattern: capture SHA at authorization time

```yaml
# In the authorization job:
- name: Authorize and capture SHA
  id: auth
  run: |
    # ... authorization checks ...
    head_sha=$(gh api "repos/$REPO/pulls/$PR_NUMBER" --jq '.head.sha')
    echo "head_sha=$head_sha" >> "$GITHUB_OUTPUT"

# Pass the pinned SHA to the reusable workflow:
uses: ./.github/workflows/test.yaml
with:
  ref: ${{ needs.auth.outputs.head_sha }}
```

### Vulnerable pattern: mutable ref after authorization

```yaml
# VULNERABLE — ref resolves at checkout time, not authorization time:
- uses: actions/checkout@v4
  with:
    ref: refs/pull/${{ github.event.issue.number }}/head
```

### Real example in this repo

`slash-test.yml` captures `.head.sha` during authorization (`steps.parse`)
and passes it as `head_sha` to both `mpc-test.yaml` and `test-e2e.yml` via
their `ref` input. The reusable workflows check out that exact SHA. This
prevents a malicious push from changing the tested code after the commenter
was authorized.

### Review checklist

- [ ] Authorization and code execution use the same SHA, captured once
- [ ] No step between authorization and execution re-resolves a mutable ref
- [ ] The captured SHA is validated (40-character hex) before use
- [ ] Reusable workflows receive the pinned SHA as an input, not a branch name

## 2. `pull_request_target` Security Model

Workflows triggered by `pull_request_target` run in the **base repository's
context** with access to base repo secrets and the OIDC token, but the event
payload contains the PR head ref and SHA.

### Key risks

- **Checking out PR head code in a `pull_request_target` workflow** gives
  untrusted code access to base repo secrets. This is the most common CI/CD
  supply-chain attack vector.
- **Trusting `github.event.pull_request.head.sha`** without authorization:
  any external contributor can open a PR, so the head SHA is attacker-controlled
  unless the workflow verifies trust first.

### Safe patterns

| Pattern | When to use |
|---------|-------------|
| Never check out PR head code | When the workflow only needs base repo files (labeling, commenting) |
| Check out PR head in a sandboxed job with no secrets | When you need to read PR files but not run them |
| Authorize the PR author before checkout | When you must run PR code (like `/test`) |

### What to flag

- `pull_request_target` + `actions/checkout` with `ref: ${{ github.event.pull_request.head.ref }}`
  or `ref: ${{ github.event.pull_request.head.sha }}` in a job that has access
  to secrets (either via `secrets: inherit` or explicit secret references).
- Any `pull_request_target` workflow that runs `make`, `go test`, `npm test`, or
  similar build commands after checking out PR code — these execute arbitrary
  code from the PR.

## 3. Secret Exposure Scope

### `secrets: inherit` vs explicit forwarding

`secrets: inherit` passes **every repository secret** to the called workflow.
This is dangerous when the called workflow runs untrusted PR code.

```yaml
# DANGEROUS — all secrets available to PR code:
uses: ./.github/workflows/test.yaml
secrets: inherit

# SAFE — only the required secret is forwarded:
uses: ./.github/workflows/test.yaml
secrets:
  CODECOV_TOKEN: ${{ secrets.CODECOV_TOKEN }}
```

### What to check

- **Which secrets does the callee actually need?** Forward only those.
  `mpc-test.yaml` needs `CODECOV_TOKEN`; `test-e2e.yml` uses OIDC (no secrets).
- **Does the job run untrusted code?** Jobs that execute `make build`, `make test`,
  or any PR-controlled code should never receive secrets beyond what they
  demonstrably need.
- **`persist-credentials: false` on checkout.** When checking out PR code,
  disable credential persistence so the checked-out code cannot access the
  `GITHUB_TOKEN` via the git credential helper.

### Permissions scoping

Apply least-privilege permissions at the job level, not just the workflow level:

```yaml
permissions: {}  # Workflow-level: deny all by default

jobs:
  auth:
    permissions:
      contents: read      # Only what this job needs
      pull-requests: read
```

Jobs that run untrusted PR code should have minimal permissions —
`contents: read` at most. Jobs that need OIDC tokens (`id-token: write`) or
write permissions should not also run untrusted code.

## 4. Workflow Command Injection

Untrusted inputs interpolated into `run:` steps can execute arbitrary commands.

### Dangerous inputs

Any value controlled by an external actor:

- `github.event.comment.body` — PR/issue comment text
- `github.event.issue.title` — issue title
- `github.event.pull_request.title` — PR title
- `github.event.pull_request.head.ref` — branch name
- `github.event.pull_request.body` — PR body

### Vulnerable pattern: inline interpolation

```yaml
# VULNERABLE — comment body is shell code:
- run: echo "Processing: ${{ github.event.comment.body }}"

# VULNERABLE — branch name can contain shell metacharacters:
- run: git checkout ${{ github.event.pull_request.head.ref }}
```

### Correct pattern: environment variables

```yaml
# SAFE — value is data, not code:
- env:
    COMMENT_BODY: ${{ github.event.comment.body }}
  run: echo "Processing: $COMMENT_BODY"
```

### What to look for

- Any `${{ }}` expression inside a `run:` block that references untrusted
  event data. These should be passed through `env:` instead.
- `::set-output::` and `::set-env::` (deprecated but still functional) with
  untrusted inputs — these can inject workflow commands.
- Untrusted inputs used in `if:` conditions that contain `contains()` or
  `startsWith()` — these are generally safe (expression evaluation, not shell),
  but verify they are not passed to shell steps.

### Real example in this repo

`slash-test.yml` passes `github.event.comment.body` through the `COMMENT_BODY`
environment variable and never interpolates it inline. The `COMMENT_USER_LOGIN`
is also passed via `env:`.

## 5. Ref Pinning for Reusable Workflows

When a workflow calls a reusable workflow via `workflow_call` and passes a `ref`
input, the callee must use that exact ref for checkout — not re-resolve a
mutable reference.

### Callee contract

```yaml
# In the reusable workflow:
on:
  workflow_call:
    inputs:
      ref:
        description: Git ref or commit SHA to test
        type: string
        required: false

# Checkout must use the provided ref:
- uses: actions/checkout@v4
  with:
    ref: ${{ inputs.ref || github.sha }}
```

### What to verify

- **Callee honors the `ref` input.** If the reusable workflow accepts a `ref`
  input but ignores it in the checkout step, the TOCTOU protection is defeated.
- **Default fallback is safe.** When `ref` is not provided (standalone triggers
  like `push` or `pull_request`), the callee should fall back to `github.sha`
  or the event's natural ref — not `refs/pull/N/head`.
- **Caller passes an immutable ref.** The caller (authorization workflow) must
  pass a full commit SHA, not a branch name or `refs/pull/N/head`.

### Concurrency groups with pinned refs

When a reusable workflow uses concurrency groups, include the pinned ref so
runs for different commits do not cancel each other:

```yaml
concurrency:
  group: ${{ github.workflow }}-${{ inputs.ref || github.ref }}
  cancel-in-progress: true
```

## Quick Reference

| Vulnerability | Check | Fix |
|---------------|-------|-----|
| TOCTOU in auth flow | Authorization and checkout use different refs | Capture SHA at auth time, pass to all downstream jobs |
| `pull_request_target` checkout | PR head checked out with secrets available | Authorize PR author first, or sandbox the checkout |
| `secrets: inherit` | Called workflow runs untrusted code with all secrets | Forward only required secrets explicitly |
| Command injection | `${{ }}` with untrusted input inside `run:` | Pass through `env:` variables instead |
| Unpinned reusable workflow ref | Callee ignores `ref` input or uses mutable ref | Callee must `checkout` with `inputs.ref` |
| Overprivileged permissions | Job has write permissions it does not need | Set `permissions: {}` at workflow level, grant per-job |

## Review Checklist

When reviewing a PR that modifies `.github/workflows/*.yml`:

- [ ] **TOCTOU:** If the workflow authorizes then executes, the SHA is pinned at authorization time
- [ ] **`pull_request_target`:** PR code is not checked out in a context with secrets unless authorized
- [ ] **Secrets:** Only required secrets are forwarded; `secrets: inherit` is not used with untrusted code
- [ ] **Injection:** No untrusted event data is interpolated in `run:` blocks; `env:` is used instead
- [ ] **Ref pinning:** Reusable workflows use the provided `ref` input for checkout
- [ ] **Permissions:** Workflow-level `permissions: {}`, job-level grants follow least privilege
- [ ] **`persist-credentials`:** Checkout steps that fetch untrusted code set `persist-credentials: false`
