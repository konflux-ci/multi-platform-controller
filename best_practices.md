
<b>Pattern 1: Keep cleanup and metric updates ordered around the actual success boundary: only decrement gauges / record durations / delete external resources after the corresponding state update (e.g., TaskRun Update, instance termination) has succeeded, so failures don’t corrupt observability or leave partial cleanup.
</b>

Example code before:
```
// cleanup
deallocateHost()
runningGauge.Dec()              // decremented even if update fails
observeDuration(time.Now().Unix() - tr.Created.Unix())
err := k8sClient.Update(ctx, tr) // may fail afterwards
return err
```

Example code after:
```
// cleanup
if err := deallocateHost(); err != nil { return err }
if err := k8sClient.Update(ctx, tr); err != nil { return err }
runningGauge.Dec()
observeDuration(time.Since(tr.CreationTimestamp.Time).Seconds())
return nil
```

<details><summary>Examples for relevant past discussions:</summary>

- https://github.com/konflux-ci/multi-platform-controller/pull/510#discussion_r2301568111
- https://github.com/konflux-ci/multi-platform-controller/pull/510#discussion_r2301593656
- https://github.com/konflux-ci/multi-platform-controller/pull/744#discussion_r2742852248
</details>


___

<b>Pattern 2: Preserve controller idempotency by using deterministic child resource names (or safe truncation helpers) rather than .metadata.generateName for resources that may be retried, and ensure generated names always respect Kubernetes length limits.
</b>

Example code before:
```
child := &tektonv1.TaskRun{
  ObjectMeta: metav1.ObjectMeta{
    GenerateName: parent.Name + "-cleanup-",
  },
}
return c.Create(ctx, child) // retries can create multiple children
```

Example code after:
```
child := &tektonv1.TaskRun{
  ObjectMeta: metav1.ObjectMeta{
    Name: kmeta.ChildName(parent.Name, "-cleanup"),
  },
}
// CreateOrUpdate / get-or-create pattern keeps retries idempotent
return ensureExists(ctx, child)
```

<details><summary>Examples for relevant past discussions:</summary>

- https://github.com/konflux-ci/multi-platform-controller/pull/618#discussion_r2519909656
- https://github.com/konflux-ci/multi-platform-controller/pull/618#discussion_r2521858285
</details>


___

<b>Pattern 3: Use retry-on-conflict update helpers (fetch/merge/retry) when updating Kubernetes objects that are also modified by other controllers (e.g., Tekton), and keep error handling consistent by returning/propagating errors instead of logging-only.
</b>

Example code before:
```
// susceptible to conflicts when Tekton updates status/metadata
if err := c.Update(ctx, tr); err != nil {
  log.Error(err, "update failed")
}
// continues despite failed update
metrics.Inc()
return nil
```

Example code after:
```
if err := UpdateWithRetry(ctx, c, apiReader, tr); err != nil {
  return fmt.Errorf("failed to update TaskRun: %w", err)
}
metrics.Inc()
return nil
```

<details><summary>Examples for relevant past discussions:</summary>

- https://github.com/konflux-ci/multi-platform-controller/pull/524#discussion_r2316444123
- https://github.com/konflux-ci/multi-platform-controller/pull/524#discussion_r2316418572
- https://github.com/konflux-ci/multi-platform-controller/pull/510#discussion_r2301568111
</details>


___

<b>Pattern 4: Ensure tests are isolated and runnable independently: set up required shared state in BeforeEach, avoid reliance on prior specs, and move reusable helpers/mocks into dedicated helper test files to keep suites maintainable.
</b>

Example code before:
```
It("increments metric", func() {
  // assumes previous test created platform metrics / objects
  runScenario()
  Expect(counter()).To(Equal(1))
})
```

Example code after:
```
BeforeEach(func() {
  resetMetrics()
  seedObjects()
})
It("increments metric", func() {
  runScenario()
  Expect(counter()).To(Equal(1))
})
```

<details><summary>Examples for relevant past discussions:</summary>

- https://github.com/konflux-ci/multi-platform-controller/pull/524#discussion_r2316435221
- https://github.com/konflux-ci/multi-platform-controller/pull/514#discussion_r2308342579
- https://github.com/konflux-ci/multi-platform-controller/pull/501#discussion_r2245434709
</details>


___

<b>Pattern 5: Prefer idiomatic, stable test assertions and patterns: use Gomega’s .Should(Succeed()) / .Error() chaining for error returns, simplify assertions to intent-focused matchers (e.g., HaveKey, ContainSubstring), and use table-driven tests to reduce duplication.
</b>

Example code before:
```
val, err := fn()
Expect(err).ShouldNot(HaveOccurred())
Expect(val).To(Equal(want))
Expect(secret.Data["error"]).Should(BeEmpty())
```

Example code after:
```
Expect(fn()).To(Succeed())
Expect(secret.Data).ShouldNot(HaveKey("error"))
Expect(err).To(MatchError(ContainSubstring("invalid characters")))
```

<details><summary>Examples for relevant past discussions:</summary>

- https://github.com/konflux-ci/multi-platform-controller/pull/501#discussion_r2245434709
- https://github.com/konflux-ci/multi-platform-controller/pull/491#discussion_r2187967757
- https://github.com/konflux-ci/multi-platform-controller/pull/524#discussion_r2316418572
- https://github.com/konflux-ci/multi-platform-controller/pull/625#discussion_r2535694975
- https://github.com/konflux-ci/multi-platform-controller/pull/512#discussion_r2306497742
</details>


___

<b>Pattern 6: Centralize and reuse normalization/validation and expensive constructs: normalize platform labels at all entry points (register + lookup), compile regexes once at package scope, and extract repeated constants/magic indexes out of functions for readability and performance.
</b>

Example code before:
```
func Handle(platform string) {
  // sometimes called with "linux/arm64", sometimes "linux-arm64"
  p := metricsMap[platform]
  if p != nil { ... }
}
func validate(s string) bool {
  re := regexp.MustCompile(pattern) // compiled every call
  return re.MatchString(s)
}
```

Example code after:
```
func Handle(platform string) {
  platform = platformLabel(platform) // normalize consistently
  if p := metricsMap[platform]; p != nil { ... }
}
var re = regexp.MustCompile(pattern)
func validate(s string) bool { return re.MatchString(s) }
```

<details><summary>Examples for relevant past discussions:</summary>

- https://github.com/konflux-ci/multi-platform-controller/pull/502#discussion_r2250599393
- https://github.com/konflux-ci/multi-platform-controller/pull/625#discussion_r2535685609
- https://github.com/konflux-ci/multi-platform-controller/pull/480#discussion_r2142701118
- https://github.com/konflux-ci/multi-platform-controller/pull/618#discussion_r2521858285
</details>


___

<b>Pattern 7: Avoid leaking sensitive identifiers in logs and align operational loops with real system constraints: obfuscate usernames/keys in shell logs, place sleeps/retries correctly, and tune periodic exporters/cleanup steps based on scrape rates, cache usage, and race-safe shared state.
</b>

Example code before:
```
echo "Deleting user $USERNAME on $SSH_HOST"
for i in {10..1}; do
  userdel "$USERNAME" || true
  sleep 1   # sleeps even after last attempt
done
ticker := time.NewTicker(15 * time.Second) // hard-coded without rationale
```

Example code after:
```
obfuscated="${USERNAME:0:2}**********${USERNAME: -3}"
for i in {10..1}; do
  if userdel "$USERNAME"; then
    echo "Deleted $obfuscated"
    exit 0
  fi
  if [ "$i" -gt 1 ]; then sleep 1; fi
done
// choose tick based on Prometheus scrape interval / config
ticker := time.NewTicker(scrapeInterval)
```

<details><summary>Examples for relevant past discussions:</summary>

- https://github.com/konflux-ci/multi-platform-controller/pull/613#discussion_r2474661367
- https://github.com/konflux-ci/multi-platform-controller/pull/613#discussion_r2474669617
- https://github.com/konflux-ci/multi-platform-controller/pull/616#discussion_r2501987571
- https://github.com/konflux-ci/multi-platform-controller/pull/490#discussion_r2174911763
</details>


___
