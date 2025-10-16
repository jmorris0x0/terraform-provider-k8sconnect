# Runnable Examples Analysis

Analysis of markdown-embedded examples to identify which could be tested automatically.

## Legend
- ✅ **RUNNABLE** - Complete example that works with k3d cluster
- ⚠️ **RUNNABLE WITH SETUP** - Needs additional resources (files, metrics-server, etc.)
- ❌ **NOT RUNNABLE** - Requires external infrastructure (AWS, GKE) or is incomplete

---

## README.md

### ❌ Getting Started (lines 31-64)
- Creates EKS cluster + deploys cert-manager
- **Blocker:** Requires AWS infrastructure

### ❌ Multi-cluster deployments (lines 114-142)
- Deploys to prod (EKS) and staging clusters
- **Blocker:** Requires multiple clusters

### ✅ Multi-Document YAML (lines 150-166)
- Uses yaml_split to parse and deploy manifests
- **Status:** Complete and runnable with k3d

### ❌ Surgical Patching (lines 178-203)
- Patches EKS aws-node DaemonSet
- **Blocker:** Requires EKS cluster

### ✅ Wait for Resources and Use Status (lines 215-255)
- LoadBalancer Service + ConfigMap using status
- **Status:** Complete and runnable with k3d (servicelb provides LB IPs)

---

## docs/resources/manifest.md

### ✅ Basic Deployment (lines 14-51)
- Namespace + nginx Deployment
- **Status:** Complete and runnable

### ❌ Bootstrap EKS Cluster with Workloads (lines 53-77)
- EKS cluster creation
- **Blocker:** Requires AWS

### ⚠️ Coexisting with HPA (lines 79-141)
- Deployment + HPA with ignore_fields
- **Setup needed:** Metrics server (might already be in k3d)
- **Status:** Likely runnable

### ✅ Wait for LoadBalancer and Use Status (lines 155-199)
- LoadBalancer Service + ConfigMap
- **Status:** Complete and runnable (servicelb in k3d)

### ✅ Wait for Deployment Rollout (lines 201-240)
- Deployment with rollout wait
- **Status:** Complete and runnable

### ✅ Wait for Condition (lines 242-278)
- Deployment with condition wait
- **Status:** Complete and runnable

### ✅ Wait for Field Value (lines 280-312)
- Job with field_value wait
- **Status:** Complete and runnable

### ❌ Multi-Cluster Deployment (lines 314-345)
- Deploys to multiple clusters
- **Blocker:** Requires multiple clusters

---

## docs/resources/patch.md

### ✅ Strategic Merge Patch (lines 39-58)
- Patches coredns deployment in kube-system
- **Status:** Complete and runnable (coredns exists in k3d)

### ✅ JSON Patch (lines 64-84)
- Patches kubernetes service in default namespace
- **Status:** Complete and runnable (kubernetes service always exists)

### ✅ Merge Patch (lines 90-110)
- Patches kube-dns service
- **Status:** Complete and runnable (kube-dns exists in k3d)

### ❌ Patching EKS AWS Node DaemonSet (lines 116-152)
- Patches aws-node DaemonSet
- **Blocker:** Requires EKS cluster

---

## docs/data-sources/yaml_split.md

### ✅ Inline Content (lines 14-47)
- Splits inline YAML with namespace/configmap/secret
- **Status:** Complete and runnable

### ⚠️ File Pattern (lines 49-63)
- Loads YAML files from directory
- **Setup needed:** Create manifests/*.yaml files
- **Status:** Runnable with file setup

---

## docs/data-sources/yaml_scoped.md

### ⚠️ Dependency Ordering (lines 12-51)
- Uses file() to load manifests
- Splits into CRDs, cluster-scoped, namespaced
- **Setup needed:** Create manifests.yaml file with CRDs
- **Status:** Runnable with file setup

---

## docs/data-sources/manifest.md

### ✅ Reading Cluster Resources (lines 14-41)
- Reads kubernetes service (always exists)
- Creates ConfigMap using the data
- **Status:** Complete and runnable

### ❌ Reading Cloud Provider Resources (lines 43-70)
- Reads aws-node DaemonSet from EKS
- **Blocker:** Requires EKS cluster

---

## docs/index.md

### ❌ Example Usage (lines 14-38)
- EKS cluster + cert-manager
- **Blocker:** Requires AWS

(Auth examples are config snippets, not complete examples)

---

## Summary

### Fully Runnable (12 examples)
1. README.md: Multi-Document YAML
2. README.md: Wait for Resources
3. docs/resources/manifest.md: Basic Deployment
4. docs/resources/manifest.md: Wait for LoadBalancer
5. docs/resources/manifest.md: Wait for Deployment Rollout
6. docs/resources/manifest.md: Wait for Condition
7. docs/resources/manifest.md: Wait for Field Value
8. docs/resources/patch.md: Strategic Merge Patch
9. docs/resources/patch.md: JSON Patch
10. docs/resources/patch.md: Merge Patch
11. docs/data-sources/yaml_split.md: Inline Content
12. docs/data-sources/manifest.md: Reading Cluster Resources

### Runnable With Setup (3 examples)
1. docs/resources/manifest.md: Coexisting with HPA (needs metrics-server)
2. docs/data-sources/yaml_split.md: File Pattern (needs manifest files)
3. docs/data-sources/yaml_scoped.md: Dependency Ordering (needs manifest files)

### Not Runnable (8 examples)
All require AWS EKS or multiple clusters

---

## Recommendation

**15 out of 23 examples (65%)** could be automatically tested with proper setup.

Suggested approach:
1. Add `<!-- runnable-test -->` marker before testable examples
2. Create a test that extracts these examples and runs them
3. For "setup needed" examples, include setup steps in test
