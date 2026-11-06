# Contributing to Multi-Platform-Controller

Thank you for your interest in contributing! This guide covers everything
you need to know to submit a successful pull request, especially if you
are contributing from a fork.

## Before You Start

1. **Fork the repository** and clone your fork locally.
2. **Create a feature branch** from `main`:
   ```bash
   git checkout -b my-feature
   ```
3. **Read the project layout and conventions** in
   [AGENTS.md](AGENTS.md) to understand how the codebase is organized.

## Coding Conventions

Before pushing any change, run the following commands locally and
iterate until all pass:

```bash
make fmt    # format code
make lint   # run golangci-lint
make test   # run unit tests (includes fmt and vet)
```

- **Coverage:** aim for 100% patch coverage on new or changed lines.
  Never drop the repository below 76% overall.
- **Large PRs (>8 files changed):** split into smaller, focused PRs
  with their own tests, or pair a code PR with a dedicated test PR and
  link both in descriptions.

## Pull Request Process

### 1. Open Your PR

Fill out the [PR template](.github/PULL_REQUEST_TEMPLATE.md) completely:
Summary, Changes, Testing, Coverage, and any caveats in the Notes
section.

### 2. CI on Fork PRs

Most CI workflows (linting, unit tests, build) run automatically on
fork PRs. However, **end-to-end (e2e) tests require AWS credentials
and will skip silently on fork PRs** unless your GitHub account has
`OWNER`, `MEMBER`, or `COLLABORATOR` association with the `konflux-ci`
organization. This is a security constraint, not a bug.

**What to do:**

- After opening your PR, leave a comment asking a maintainer to review
  and trigger any additional CI checks that did not run automatically.
  You can find maintainers listed in recent merged PRs or in the
  repository's contributor list.
- Do **not** wait silently. If you have not received any response within
  a few business days, leave a follow-up comment on the PR.

### 3. Automated Review Limitations

This repository uses automated code-review agents for PRs opened from
branches within the repo. **Automated agent review does not run on fork
PRs.** Your review will come from human maintainers.

### 4. Review Timeline

- A maintainer will typically review your PR within **3-5 business
  days**.
- **One approval** from a `konflux-ci` infrastructure team member is
  required to merge; **two approvals** are preferred.
- If you have not heard back after 5 business days, leave a polite
  follow-up comment on the PR. Maintainers may be busy, and a gentle
  ping helps keep things moving.

### 5. Addressing Feedback

When you receive review comments:

- Push new commits to your PR branch to address feedback (do not
  force-push or squash during review — it makes it harder for
  reviewers to see what changed).
- Re-run `make fmt`, `make lint`, and `make test` before each push.
- Reply to review comments to explain your changes or ask for
  clarification.

## Commit Messages

Follow the repository's commit convention:

```
KFLUXINFRA-<number> <short description>
```

If your change is not tied to a Jira ticket, use a descriptive message
without the prefix. Include a `Co-Authored-By:` trailer if you paired
with someone.

## What Happens After Merge

After your PR is merged, the change follows a staged deployment process:

1. **Staging** — a maintainer approves the automated PR from the
   deployment bot.
2. **Private production** — deployed manually alongside staging.
3. **~24-hour soak** — monitored for issues.
4. **Public production** — rolled out per cluster.

You do not need to do anything for deployment. Maintainers handle the
rollout.

## Getting Help

- **Questions about an issue:** comment on the issue itself.
- **General questions:** open a new issue with the `question` label or
  comment on an existing discussion.
- **CI failures you cannot diagnose:** mention it in your PR and ask a
  maintainer for help.

We appreciate your contribution!
