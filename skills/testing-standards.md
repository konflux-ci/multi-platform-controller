---
name: testing-standards
description: >
  Use when writing or reviewing Ginkgo/Gomega test code to apply the team's
  testing conventions for structure, style, and organization in the
  multi-platform-controller codebase.
---

# Testing Standards

Ginkgo/Gomega conventions for multi-platform-controller. Apply in every test change.

## Style Rules

### 1. `When()` Not `Context()`

Always `When()` for scenario blocks, never `Context()`.

```go
When("the instance is terminated", func() { ... })       // Do
Context("when the instance is terminated", func() { ... }) // Don't
```

### 2. `Should()`/`ShouldNot()` Not `To()`/`NotTo()`

```go
Expect(err).ShouldNot(HaveOccurred())
Expect(count).Should(Equal(3))
```

### 3. Sentence Coherence — Describe/When/It Must Read as a Sentence

```go
Describe("LaunchInstance", func() {
    When("the EC2 API returns an error", func() {
        It("should return the error", func() { ... })
    })
})
// Reads: "LaunchInstance, when the EC2 API returns an error, should return the error"
```

### 4. Separate `When()` Blocks for Scenarios

Group related scenarios (happy/error/edge) into distinct `When()` blocks.

```go
Describe("GetInstanceAddress", func() {
    When("the API returns an error", func() { ... })
    When("no reservations are returned", func() { ... })
    When("multiple addresses are available", func() { ... })
})
```

### 5. Inline With Forethought

```go
// Single-return — inline freely
Expect(cfg.SshUser()).Should(Equal("ec2-user"))
// (value, error) — must capture both, can't inline
id, err := cfg.LaunchInstance(ctx, name, tag, labels)
Expect(err).ShouldNot(HaveOccurred())
// Error-only — be cautious: inlining panics if function returns nil unexpectedly
err := cfg.TerminateInstance(ctx, "i-123")
Expect(err).ShouldNot(HaveOccurred())
```

Also consider line length: if inlining creates an overly long line due to large inputs,
split the input setup from the function call for readability.

```go
// Long input — split for readability
data := map[string][]byte{"host": []byte("localhost")}
Expect(createUserTaskSecret(r, ctx, tr, "my-secret", data)).To(Succeed())
```

Don't blindly capture every return; don't blindly inline either. Think first.

### 6. `DescribeTable` for Repeated Patterns

When test cases share identical `Expect()` structure, use `DescribeTable`.

```go
DescribeTable("instance state mapping",
    func(ctx SpecContext, stateName types.InstanceStateName) {
        mock.DescribeInstancesOutput = stateOutput(stateName)
        state, err := cfg.GetState(nil, ctx, "i-123")
        Expect(err).ShouldNot(HaveOccurred())
        Expect(state).Should(Equal(cloud.OKState))
    },
    Entry("running instance", types.InstanceStateNameRunning),
    Entry("pending instance", types.InstanceStateNamePending),
)
```

### 7. Test Your Logic, Not Stdlib

Test what *your* function does, not that `context.WithCancel` or `json.Marshal` work.

```go
// Do — verify your function handles cancellation
When("the context is cancelled before launch", func() {
    It("should return a context error", func() { ... })
})
// Don't — verify stdlib behavior
Expect(ctx.Err()).Should(Equal(context.Canceled)) // tests stdlib, not your code
```

## DRY Test Setup

Consolidate shared variables into `BeforeEach`. Extract helpers when `BeforeEach`
isn't flexible enough. Never duplicate setup across `It()` blocks.

```go
BeforeEach(func() {
    mock = &mockEC2Client{}
    cfg = AWSEc2DynamicConfig{InstanceType: "t4g.medium", ec2Client: mock}
})
```

## Go-Specific Pitfalls

| Pitfall | What Happens | Prevention |
|---------|-------------|------------|
| Nil interface comparison | `var e error; e == nil` is true, but interface holding nil pointer is not nil | Compare concrete types or use `Expect().Should(BeNil())` |
| Nil map write | `var m map[string]string; m["k"] = "v"` panics | Initialize with `make()` or literal |
| Closed channel send | `close(ch); ch <- v` panics | Guard sends with select or sync |
| JSON unknown fields | `json.Unmarshal` silently ignores unknown keys | Use `DisallowUnknownFields()` decoder when strictness matters |
| Race conditions | Concurrent map/slice access passes locally, fails under `-race` | Always run `ginkgo -race` (the Makefile does this) |
| Unsafe type assertion | `v := i.(MyType)` panics if wrong | Use `v, ok := i.(MyType)` two-value form |

When writing new code that handles any of these scenarios, write temporary tests to verify
your code does not fall into these traps. These tests can be removed once the code is stable,
but they must exist during development to catch pitfalls early.

## Quick Reference

| Do | Don't |
|----|-------|
| `When("the API fails", ...)` | `Context("when the API fails", ...)` |
| `Expect(x).Should(Equal(y))` | `Expect(x).To(Equal(y))` |
| Sentence-readable Describe/When/It | `It("test case 1", ...)` |
| `DescribeTable` for repeated patterns | Copy-paste `It` blocks with one param changed |
| `BeforeEach` for shared setup | Same 5-line setup in every `It` |
| Test your function's behavior | Test that `context.WithCancel` works |
| Capture `(value, error)` returns | Inline multi-return calls into `Expect()` |
| Think before inlining error-only returns | Blindly inline everything |

## Test Organization

- **Suite files**: Each package has `*_suite_test.go` registering the Ginkgo runner.
- **Helpers and mocks**: Reusable test utilities live in `*_helpers_test.go` / `*_mock_test.go`
  (see file splitting convention in `skills/go-coding-standards.md`).
- **Reconciler tests**: Simulate full workflows with fake K8s clients in `pkg/reconciler/taskrun/`.
- **Allocation coverage**: New strategies need tests in `provision_*_test.go`.
- **Run tests**: `make test` or target a package with `ginkgo ./pkg/aws/`.
