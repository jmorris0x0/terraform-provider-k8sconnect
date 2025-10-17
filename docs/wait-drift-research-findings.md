# Wait Resource Drift: Research Findings from Terraform Documentation

## Question 1: Does `depends_on` alone cause downstream replacement?

**Answer: NO** ✅ **CONFIRMED**

**Source:** [Terraform GitHub Issue #2895](https://github.com/hashicorp/terraform/issues/2895)

**Direct Quote:**
> "If resource A depends_on resource B, A will be created after B, but if B gets tainted then nothing will happen to A (assuming no other reference to B)."

**Implications:**

```hcl
resource "k8sconnect_wait" "migration" {
  wait_for = { condition = "Complete" }
}

resource "aws_db_instance" "aurora" {
  depends_on = [k8sconnect_wait.migration]
  # NO attribute reference, only depends_on
}
```

**If wait resource is tainted and recreated:**
- Aurora DB will **NOT** be destroyed
- Aurora DB will **NOT** be recreated
- Only the wait resource is recreated (which is a no-op in K8s)

**This is EXACTLY what we want for Use Case 2 (dependency ordering).**

**Workaround for forcing recreation:** Create an implicit dependency through attribute interpolation:

```hcl
resource "aws_db_instance" "aurora" {
  tags = {
    wait_id = k8sconnect_wait.migration.id  # Now Aurora recreates if wait recreates
  }
}
```

But for ordering gates, we DON'T want this - we want the depends_on-only pattern.

---

## Question 2: Does `ignore_changes` work with computed values from other resources?

**Answer: PARTIALLY / COMPLEX** ⚠️

**Source:** [Terraform GitHub Issue #19670](https://github.com/hashicorp/terraform/issues/19670)

**What we know:**

1. **Interpolations are NOT supported in the lifecycle block itself:**
   ```hcl
   lifecycle {
     ignore_changes = ["${var.some_var}"]  # Does NOT work
   }
   ```
   Source: [Terraform GitHub Issue #5446](https://github.com/hashicorp/terraform/issues/5446)

2. **But literal attribute names with interpolated VALUES might work:**
   ```hcl
   resource "aws_db_instance" "aurora" {
     tags = {
       status = k8sconnect_wait.migration.status.phase  # Interpolation in value
     }
     lifecycle {
       ignore_changes = [tags]  # Literal attribute reference
     }
   }
   ```

3. **Known bug:** `ignore_changes` can incorrectly prevent recreation when it shouldn't (Issue #19670)
   - When attribute A has `ignore_changes` and attribute B (with `RequiresNew`) is interpolated
   - The ignore_changes logic can incorrectly skip the RequiresNew check
   - **Status:** Fixed in master branch per the issue

**What we DON'T know:**

- Does `ignore_changes = [tags]` work when `tags.status` value comes from another resource?
- What happens when the referenced value becomes null?
- What happens when the referenced value becomes unknown?
- Does it prevent replacement or just suppress drift detection?

**Needs empirical testing to confirm behavior with our specific pattern.**

---

## Question 3: Can you reference null/unknown values in Terraform?

**Answer: YES, but with consequences** ⚠️

**Source:** Multiple Stack Overflow and GitHub issues

**What we know:**

1. **Referencing unknown values is allowed:**
   - The plan will show the attribute as "(known after apply)"
   - Downstream resources that reference it will also show unknown values
   - This can cascade through the dependency graph

2. **Referencing null values is allowed:**
   - The value evaluates to null and propagates to dependent resources
   - May cause errors if the dependent resource doesn't handle null properly

3. **Unknown values can trigger unnecessary replacements:**
   - From our earlier research: "If a resource attribute is unknown during the plan phase because an upstream dependency is recreated, the resource may be destroyed first then recreated, even if the actual value is unchanged."

4. **Depends on whether the attribute is marked RequiresReplace:**
   - If the attribute consuming the null/unknown value is not marked RequiresReplace, the resource updates in-place
   - If it IS marked RequiresReplace, the resource is destroyed and recreated

---

## Question 4: ignore_changes behavior with null/unknown

**Answer: UNCLEAR** ❓

**From search results:**

1. **GitHub Issue #29496:** "terraform lifecycle ignore_changes not ignored"
   - Shows that ignore_changes doesn't always work as expected
   - Especially with computed attributes

2. **Data sources producing null instead of unknown (Issue #36653):**
   - Shows Terraform has bugs distinguishing null vs unknown in certain contexts
   - May affect how ignore_changes behaves

3. **No definitive documentation found** on whether ignore_changes:
   - Keeps the old value when new value is null
   - Keeps the old value when new value is unknown
   - Works with attributes computed from other resources

**This REQUIRES empirical testing.**

---

## Conclusions from Research

### What We Know ✅

1. **`depends_on` alone does NOT cause downstream recreation** (Use Case 2: Ordering)
   - Safe pattern for dependency gates
   - Won't destroy Aurora DB when Job is recreated

2. **`depends_on` only enforces creation order, not lifecycle tracking**
   - Need attribute interpolation for lifecycle dependencies

3. **Interpolations in lifecycle block itself don't work**
   - But `ignore_changes = [literal_attribute]` might work even if attribute value is interpolated

### What We Don't Know ❓

1. **Does `ignore_changes` work with our specific pattern?**
   ```hcl
   tags = { status = other_resource.output }
   lifecycle { ignore_changes = [tags] }
   ```

2. **What happens when referenced value becomes null?**
   - Does ignore_changes keep old value or allow null through?

3. **What happens when referenced value becomes unknown?**
   - Does ignore_changes prevent "(known after apply)" propagation?

4. **Does this prevent replacement or just drift warnings?**

### Recommendations

**For Use Case 2 (Dependency Ordering - Job → Aurora):**

**Pattern 1: Pure depends_on (SAFE, CONFIRMED TO WORK):**
```hcl
resource "k8sconnect_wait" "migration" {
  wait_for = { condition = "Complete" }  # No status per ADR-008
}

resource "aws_db_instance" "aurora" {
  depends_on = [k8sconnect_wait.migration]
  # NO attribute references
  # Aurora will NOT be destroyed if wait is tainted
}
```

**For Use Case 1 (Value Extraction - Ingress → Firewall):**

**Pattern 2: Attribute reference (drift propagates, which is DESIRED):**
```hcl
resource "k8sconnect_wait" "ingress" {
  wait_for = { field = "status.loadBalancer.ingress[0].hostname" }
  # Populates status per ADR-008
}

resource "cloudflare_record" "firewall" {
  value = k8sconnect_wait.ingress.status.loadBalancer.ingress[0].hostname
  # If hostname changes, firewall updates (DESIRED behavior)
}
```

**Pattern 3: Attribute reference + ignore_changes (NEEDS TESTING):**
```hcl
resource "aws_db_instance" "aurora" {
  tags = {
    migration_status = k8sconnect_wait.migration.status.phase
  }
  lifecycle {
    ignore_changes = [tags]
  }
  # Aurora created only after migration completes
  # After creation, changes to wait status don't affect Aurora
  # BUT: Does this actually work when status becomes null/unknown? UNKNOWN.
}
```

### Next Steps

1. ✅ **Pattern 1 (pure depends_on) is CONFIRMED to work** - Document this pattern
2. ⚠️ **Pattern 3 (ignore_changes) needs empirical testing** - But might not be necessary if Pattern 1 works
3. 📝 **Update ADR-016** with Pattern 1 as the recommended approach for ordering gates
4. 🔨 **Implement Read() status refresh** for field waits (Use Case 1)

### Do We Still Need Empirical Testing?

**For depends_on:** NO - We have definitive documentation it works as needed

**For ignore_changes:** MAYBE - Only if users specifically want Pattern 3 instead of Pattern 1

**Recommendation:** Document Pattern 1 (pure depends_on) as the solution for ordering gates, since it's proven to work and is simpler than Pattern 3.
