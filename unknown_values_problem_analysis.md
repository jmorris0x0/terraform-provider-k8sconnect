# Unknown Values Problem Analysis

## The Problem

The `terraform-provider-k8sinline` fails during Terraform planning when cluster connection details come from data sources that depend on resources not yet created.

### Error Message
```
Error: Value Conversion Error

An unexpected error was encountered trying to build a value. This is always an error in the provider. Please report the following to the provider developer:

Received unknown value, however the target type cannot handle unknown values. Use the corresponding `types` package type or a custom type that handles unknown values.

Path: cluster_connection
Target Type: manifest.ClusterConnectionModel
Suggested Type: basetypes.ObjectValue
```

### Failing Configuration
```hcl
data "aws_eks_cluster" "this" {
  name       = var.cluster_name
  depends_on = [rubix_cluster.this]  # ← This creates unknown values during planning
}

locals {
  cluster_connection = {
    host                   = data.aws_eks_cluster.this.endpoint
    cluster_ca_certificate = data.aws_eks_cluster.this.certificate_authority[0].data
    exec = {
      api_version = "client.authentication.k8s.io/v1"
      command     = "aws"
      args        = ["eks", "get-token", "--cluster-name", var.cluster_name]
    }
  }
}

module "reflector" {
  source = "./modules/reflector"
  cluster_connection = local.cluster_connection  # ← Unknown values passed here
}
```

## The Goal

**Primary Objective**: Enable the k8sinline provider to handle unknown values during Terraform planning, exactly like the kubectl provider does.

**Secondary Objectives**:
1. Plan must complete successfully without errors
2. Apply must work correctly when values become known
3. No architectural compromises (no separate applies, no workarounds)

## Critical Discovery: kubectl Provider Uses Plugin SDK v2

### Key Insight: Different Plugin Architectures
The gavinbunney kubectl provider that works successfully uses **Terraform Plugin SDK v2**, while our k8sinline provider uses **Terraform Plugin Framework**. This is a fundamental architectural difference that explains why they handle unknown values differently.

**kubectl provider (working):**
```go
// Uses Plugin SDK v2
if !d.NewValueKnown("yaml_body") {
    log.Printf("[TRACE] yaml_body value interpolated, skipping customized diff")
    return nil
}
```

**k8sinline provider (failing):**
```go
// Uses Plugin Framework with concrete structs
type ClusterConnectionModel struct {
    Host                 types.String   `tfsdk:"host"`
    ClusterCACertificate types.String   `tfsdk:"cluster_ca_certificate"`
    // ... 
}
```

### Architecture Comparison
- **Plugin SDK v2**: Uses `*schema.ResourceData` which naturally handles unknown values
- **Plugin Framework**: Uses strongly-typed structs that must explicitly support unknown values

### The Real Solution
We have two paths forward:

1. **Stay with Plugin Framework**: Implement proper unknown value handling in our schema/models
2. **Switch to Plugin SDK v2**: Rewrite the provider to match kubectl's working architecture

Given the kubectl provider's proven success with your exact use case, option 2 might be the most reliable path.

### Working Reference: kubectl Provider
The kubectl provider successfully handles this exact scenario:
```hcl
provider "kubectl" {
  alias                  = "this"
  host                   = data.aws_eks_cluster.this.endpoint      # Unknown during planning
  cluster_ca_certificate = base64decode(data.aws_eks_cluster.this.certificate_authority[0].data)
  exec {
    api_version = "client.authentication.k8s.io/v1beta1"
    args        = ["eks", "get-token", "--cluster-name", var.cluster_name]
    command     = "aws"
  }
  load_config_file = false
}
```

### Root Cause Analysis
1. **Timing**: During planning, `rubix_cluster.this` doesn't exist yet
2. **Dependency Chain**: `data.aws_eks_cluster.this` depends on the non-existent cluster
3. **Unknown Values**: Terraform core sets cluster endpoint/CA as unknown during planning
4. **Framework Limitation**: Our `ClusterConnectionModel` struct cannot handle unknown values
5. **Schema Issue**: Using `schema.SingleNestedAttribute` with concrete struct instead of flexible object type

### Current Implementation
```go
type ClusterConnectionModel struct {
    Host                 types.String   `tfsdk:"host"`
    ClusterCACertificate types.String   `tfsdk:"cluster_ca_certificate"`
    // ... other fields
}

// Schema uses:
"cluster_connection": schema.SingleNestedAttribute{
    // Binds directly to ClusterConnectionModel
}
```

### Framework Error Details
- **Error Location**: Terraform Plugin Framework type conversion
- **Error Timing**: During planning phase when deserializing config
- **Framework Suggestion**: Use `basetypes.ObjectValue` instead of concrete struct
- **Core Issue**: Schema cannot represent unknown values with current structure

## Attempted Solutions

### Failed Approach: Simple Test Reproduction
- **Tried**: Variables without defaults to create unknown values
- **Result**: Test failed - variables just triggered validation errors
- **Learning**: Real unknown values require actual dependency chains, not missing variables

### Theoretical Solution Components
1. **Schema Change**: Switch from `SingleNestedAttribute` to `ObjectAttribute`
2. **Model Change**: Use `types.Object` in resource model instead of concrete struct
3. **Runtime Handling**: Convert from `types.Object` to `ClusterConnectionModel` only when values are known
4. **Planning Logic**: Skip K8s operations during planning when values are unknown

## Critical Constraints

### Non-Negotiable Requirements
- **No separate applies**: Must work in single `terraform plan && terraform apply`
- **No architectural workarounds**: The provider must handle unknown values natively
- **Compatibility**: Must work exactly like kubectl provider for this use case

### Technical Constraints
- **Time Investment**: 8+ hours already spent on this specific issue
- **Testing Difficulty**: Cannot easily reproduce unknown value scenarios in unit tests
- **Validation Required**: Changes must be tested with real Terraform workflow

## Success Criteria

1. **Planning**: `terraform plan` completes without errors when cluster connection has unknown values
2. **Application**: `terraform apply` successfully creates resources when values become known  
3. **State Management**: Proper state transitions from unknown to known values
4. **Compatibility**: Existing working configurations continue to work

## Next Steps Decision Point

The solution requires deep changes to:
- Schema definition (ObjectAttribute vs SingleNestedAttribute)
- Resource model structure (types.Object vs concrete struct)
- Runtime value handling (deferred client creation)
- Planning phase behavior (skip operations for unknown values)

**Key Question**: Is the team confident enough in the theoretical solution to implement without a comprehensive test that reproduces the exact error scenario?