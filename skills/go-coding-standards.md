---
name: go-coding-standards
description: >
  Use when writing or reviewing Go code to apply the team's coding standards
  for simplicity, readability, performance, and maintainability in the
  multi-platform-controller codebase.
---

# Go Coding Standards

Team preferences for writing Go in multi-platform-controller. Apply in every code change.

## Code Style

### 1. Fewer Lines, Better Readability

Eliminate unnecessary variables and boilerplate.
Avoid comments on unexported helpers whose name already explains intent.

```go
// Prefer
return ctrl.Result{RequeueAfter: time.Minute}, nil

// Avoid
result := ctrl.Result{RequeueAfter: time.Minute}
return result, nil
```

### 2. DRY — Extract Repeated Logic into Helpers

```go
func labelSelector(key, value string) client.MatchingLabels {
    return client.MatchingLabels{key: value}
}
```

### 3. Modularize — Small, Reusable Pieces

One concept per file. Break large functions into focused helpers.

```go
// dynamic.go  — allocation logic
// hostpool.go — pool lifecycle management
// Each file owns one responsibility; changes stay isolated.
```

**File splitting convention:** when a file grows large, split into `X.go` and `X_helpers.go`.
`X.go` keeps the workflow-specific functions (single-caller, tied to a specific flow).
`X_helpers.go` keeps reusable utility functions (multi-caller or written for DRY purposes).
The same pattern applies to test files: `X_test.go` and `X_helpers_test.go`.

### 4. Single Responsibility — One Function, One Task

```go
func (r *ReconcileTaskRun) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
    tr := &tektonapi.TaskRun{}
    if err := r.client.Get(ctx, req.NamespacedName, tr); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    return r.handleTaskRunReceived(ctx, tr)
}
```

### 5. Return Early — Guard Clauses Over Nesting

Fail fast at the top; keep the happy path at the lowest indentation level.

```go
func (r *ReconcileTaskRun) provisionHost(ctx context.Context, tr *tektonapi.TaskRun) error {
    if tr.Status.CompletionTime != nil {
        return nil // already finished
    }
    if tr.Labels == nil {
        return fmt.Errorf("taskrun %s has no labels", tr.Name)
    }
    // ... happy path continues at base indentation
}
```

## Performance and Memory

Reconcile loops run frequently under load — these patterns matter.

| Guideline | Detail |
|-----------|--------|
| **Avoid allocations in hot paths** | Don't create slices/maps inside tight loops; reuse or lift outside. |
| **Pre-allocate when size is known** | `make([]string, 0, len(items))` instead of `var s []string`. |
| **Pointer receivers for large structs** | `ReconcileTaskRun` methods use pointer receivers to avoid copying. |
| **Respect `ctx.Done()`** | Check context cancellation in long-running cloud provider calls. |
| **Prevent goroutine leaks** | Always pair goroutines with context or done-channel cleanup. |
| **Bounded retries** | Never retry indefinitely. Use `RequeueAfter` with backoff or a max-attempt cap. |

## Quick Reference

| Principle | Do | Don't |
|-----------|----|-------|
| Brevity | `return ctrl.Result{}, err` | Assign to temp var, then return |
| DRY | Helper for repeated label selectors | Copy-paste the same 3 lines |
| Modularity | One strategy per file | 500-line god function |
| Single responsibility | Reconciler dispatches, helper executes | Reconciler does everything inline |
| Guard clauses | `if err != nil { return }` at top | Deeply nested if/else chains |
| Allocations | `make([]v1.TaskRun, 0, count)` | `var trs []v1.TaskRun` inside a loop |
| Context | `select { case <-ctx.Done(): }` | Blocking call with no cancellation |
| Retries | `RequeueAfter: backoff` | `for { retry() }` |

For Go-specific pitfalls to avoid (nil interfaces, nil map writes, unsafe type assertions, etc.),
see the Go-Specific Pitfalls table in `skills/testing-standards.md`.

## Review Checklist

Before submitting code, verify:

- [ ] No unnecessary comments on unexported functions with clear names
- [ ] No duplicated logic that should be a helper
- [ ] Functions each have a single, clear responsibility
- [ ] Guard clauses used instead of deep nesting
- [ ] Slices/maps pre-allocated when capacity is known
- [ ] Context cancellation respected in cloud/network calls
- [ ] Retry loops are bounded (max attempts or `RequeueAfter`)
