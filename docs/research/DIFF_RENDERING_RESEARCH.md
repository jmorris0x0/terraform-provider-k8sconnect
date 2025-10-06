# Terraform Plugin Framework: Diff Rendering Research

## Executive Summary

This document analyzes how Terraform handles diff rendering for plan output and evaluates the feasibility of adding custom diff formatting capabilities to terraform-plugin-framework.

**Key Finding:** Diff rendering happens entirely in Terraform Core, not in the provider framework. The framework only sends opaque binary data (DynamicValue) to Core via Protocol Buffers. There are currently **no extension points** for providers to customize how their attribute changes are displayed to users.

## Current Architecture

### 1. Data Flow: Provider to CLI Output

```
Provider (terraform-plugin-framework)
    |
    | Converts framework types to tftypes.Value
    v
terraform-plugin-go (Protocol Layer)
    |
    | Encodes as DynamicValue (msgpack/json)
    | via Protocol Buffers (tfprotov6)
    v
Terraform Core
    |
    | Decodes DynamicValue
    | Compares prior_state vs planned_state
    v
Diff Computation (internal/command/jsonformat/differ)
    |
    | Routes by cty.Type to type-specific renderers
    v
Diff Rendering (internal/command/jsonformat/computed/renderers)
    |
    | Generates human-readable output
    v
CLI Display
```

### 2. Protocol Buffer Contract

The provider-to-core communication uses the tfplugin6 protocol defined in:
- **File**: `/tmp/terraform-plugin-go/tfprotov6/internal/tfplugin6/tfplugin6.proto`
- **Protocol Version**: 6.10

#### Key Message Types

```protobuf
// DynamicValue is an opaque encoding of terraform data
message DynamicValue {
    bytes msgpack = 1;
    bytes json = 2;
}

message PlanResourceChange {
    message Request {
        string type_name = 1;
        DynamicValue prior_state = 2;
        DynamicValue proposed_new_state = 3;
        DynamicValue config = 4;
        bytes prior_private = 5;
        DynamicValue provider_meta = 6;
        // ...
    }

    message Response {
        DynamicValue planned_state = 1;
        repeated AttributePath requires_replace = 2;
        bytes planned_private = 3;
        repeated Diagnostic diagnostics = 4;
        // ...
    }
}
```

**Critical Limitation**: The `DynamicValue` is an opaque binary blob (msgpack or json encoded). It contains no metadata about how to render the data - only the raw values and their types.

### 3. Framework to Protocol Conversion

**File**: `/tmp/terraform-plugin-framework/internal/toproto6/dynamic_value.go`

```go
func DynamicValue(ctx context.Context, data *fwschemadata.Data) (*tfprotov6.DynamicValue, diag.Diagnostics) {
    // ...
    proto6, err := tfprotov6.NewDynamicValue(data.Schema.Type().TerraformType(ctx), data.TerraformValue)
    // ...
}
```

The framework converts its internal representation to `tftypes.Value`, then encodes it into a `DynamicValue`. No rendering hints or display metadata is included.

### 4. Core's Diff Rendering Logic

**Location**: `/tmp/terraform/internal/command/jsonformat/`

#### Type-Based Routing

**File**: `differ/attribute.go`

```go
func ComputeDiffForType(change structured.Change, ctype cty.Type) computed.Diff {
    // ...
    switch {
    case ctype.IsPrimitiveType():
        return computeAttributeDiffAsPrimitive(change, ctype)
    case ctype.IsObjectType():
        return computeAttributeDiffAsObject(change, ctype.AttributeTypes())
    case ctype.IsMapType():
        return computeAttributeDiffAsMap(change, ctype.ElementType())
    case ctype.IsListType():
        return computeAttributeDiffAsList(change, ctype.ElementType())
    case ctype.IsSetType():
        return computeAttributeDiffAsSet(change, ctype.ElementType())
    // ...
    }
}
```

#### Renderer Interface

**File**: `computed/diff.go`

```go
type DiffRenderer interface {
    RenderHuman(diff Diff, indent int, opts RenderHumanOpts) string
    WarningsHuman(diff Diff, indent int, opts RenderHumanOpts) []string
}
```

**Different renderers for each type**:
- `primitiveRenderer` - strings, numbers, bools
- `mapRenderer` - map attributes
- `listRenderer` - list attributes
- `setRenderer` - set attributes
- `objectRenderer` - object/block attributes
- `sensitiveRenderer` - wraps sensitive values
- `unknownRenderer` - handles computed values

### 5. Why String vs Map Renders Differently

From `differ/attribute.go`, we can see:

**String Attribute** (Primitive):
```go
case ctype.IsPrimitiveType():
    return computeAttributeDiffAsPrimitive(change, ctype)
```

Renders as:
```
  ~ attribute_name = "old" -> "new"
```

**Map Attribute**:
```go
case ctype.IsMapType():
    return computeAttributeDiffAsMap(change, ctype.ElementType())
```

From `differ/map.go`:
```go
func computeAttributeDiffAsMap(change structured.Change, elementType cty.Type) computed.Diff {
    mapValue := change.AsMap()
    elements, current := collections.TransformMap(mapValue.Before, mapValue.After, mapValue.AllKeys(), func(key string) computed.Diff {
        value := mapValue.GetChild(key)
        return ComputeDiffForType(value, elementType)
    })
    return computed.NewDiff(renderers.Map(elements), current, change.ReplacePaths.Matches())
}
```

Renders as (expanded, showing individual keys):
```
  ~ attribute_name = {
      ~ key1 = "old" -> "new"
      + key2 = "added"
      - key3 = "removed"
    }
```

The rendering is **hardcoded based on the cty.Type**, not provider-controlled.

## Existing Extension Points

### 1. PlanModifiers

**File**: `/tmp/terraform-plugin-framework/resource/schema/planmodifier/string.go`

```go
type String interface {
    Describer
    PlanModifyString(context.Context, StringRequest, *StringResponse)
}

type StringRequest struct {
    Path path.Path
    Config tfsdk.Config
    ConfigValue types.String
    Plan tfsdk.Plan
    PlanValue types.String
    State tfsdk.State
    StateValue types.String
    Private *privatestate.ProviderData
}

type StringResponse struct {
    PlanValue types.String          // Can modify the value
    RequiresReplace bool             // Can force replacement
    Private *privatestate.ProviderData
    Diagnostics diag.Diagnostics
}
```

**Capabilities**:
- Modify the planned value
- Mark attribute as requiring replacement
- Store private data
- Return diagnostics

**Limitations**:
- **Cannot** control how the diff is rendered
- **Cannot** add rendering hints or metadata
- Only affects the plan data, not the display

### 2. Sensitive Attribute Flag

The `Sensitive` field on attributes is the **only** rendering customization available:

```go
type StringAttribute struct {
    // Sensitive indicates whether the value should be obscured in CLI output
    Sensitive bool
    // ...
}
```

This works because:
1. The schema (including `Sensitive` flag) is sent to Core via `GetProviderSchema` RPC
2. Core checks the schema when rendering
3. If sensitive, it uses `sensitiveRenderer` which displays `(sensitive value)`

**File**: Core's schema definition in protocol:
```protobuf
message Schema {
    message Attribute {
        string name = 1;
        bytes type = 2;
        bool sensitive = 7;
        // ...
    }
}
```

### 3. Custom Types

Providers can define custom attribute types:

```go
type StringAttribute struct {
    CustomType basetypes.StringTypable
    // ...
}
```

However, custom types only affect:
- How values are stored/retrieved in provider code
- Validation logic
- **NOT** how diffs are rendered (still uses primitive/map/list renderers based on underlying cty.Type)

## Relevant GitHub Issues

### terraform-plugin-framework

1. **Issue #921** - "Plan output not displayed or displays null for explicit empty string input"
   - Shows rendering issues but no capability to fix from provider side

2. **Issue #70** - "Classifying normalization vs. drift"
   - Discusses lack of `DiffSuppressFunc` equivalent
   - DiffSuppressFunc could hide diffs, but not customize rendering

3. **Issue #1006** - "Diagnostics.AddAttributeWarning does not redact value for sensitive attribute"
   - Shows that `Sensitive` flag affects rendering
   - But it's the only rendering control available

### terraform (core)

1. **PR #26187** - "Add experimental concise diff renderer"
   - Shows Core owns all rendering logic
   - Providers have no input into this

2. **Issue #28947** - "Improve yamlencode/jsonencode sensitive output redaction"
   - Entire diff gets redacted when sensitive values present
   - Provider cannot control granularity

## What Would Need to Change

To enable custom diff rendering in providers, the following changes would be required:

### 1. Protocol Changes (Breaking!)

**Add rendering metadata to protocol**:

```protobuf
message AttributeRenderingHint {
    enum RenderStyle {
        DEFAULT = 0;
        COMPACT = 1;      // Single-line even for maps/objects
        EXPANDED = 2;     // Always show all keys
        CUSTOM = 3;       // Provider supplies formatted string
    }
    RenderStyle style = 1;
    optional string custom_format = 2;  // If style == CUSTOM
}

message Schema {
    message Attribute {
        string name = 1;
        bytes type = 2;
        bool sensitive = 7;
        AttributeRenderingHint rendering = 11;  // NEW
        // ...
    }
}
```

**Problem**: This is a major protocol change requiring:
- New tfprotov7 protocol version
- Terraform Core update
- Framework update
- All providers need to update
- Breaking change to ecosystem

### 2. Framework Changes

**Add rendering configuration to schema attributes**:

```go
type StringAttribute struct {
    // ... existing fields

    // RenderingHint controls how diffs are displayed
    RenderingHint AttributeRenderingHint
}

type AttributeRenderingHint struct {
    Style RenderStyle
    CustomFormatter func(before, after types.String) string
}
```

**Challenges**:
- Custom formatter functions can't be serialized over RPC
- Would need to be evaluated in provider, sent as string to Core
- Core would need to trust/sanitize provider-supplied strings

### 3. Core Changes

**Modify diff renderer routing**:

```go
func ComputeDiffForAttribute(change structured.Change, attribute *jsonprovider.Attribute) computed.Diff {
    // Check for rendering hint in schema
    if attribute.RenderingHint != nil {
        switch attribute.RenderingHint.Style {
        case COMPACT:
            return renderers.Compact(change)
        case CUSTOM:
            return renderers.Custom(change, attribute.RenderingHint.CustomFormat)
        }
    }

    // Fall back to existing type-based routing
    return ComputeDiffForType(change, unmarshalAttribute(attribute))
}
```

## Alternative Approaches

### Option 1: String-Based Hack (Current Workaround)

**What users do now**: Store formatted data as a string attribute instead of structured data.

```go
// Instead of:
"labels": schema.MapAttribute{
    ElementType: types.StringType,
    Computed: true,
}

// Do:
"labels_display": schema.StringAttribute{
    Computed: true,
    Description: "Formatted labels for display",
}
```

**Provider code**:
```go
labelsDisplay := formatLabelsCompact(labels)  // Provider does formatting
resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("labels_display"), labelsDisplay)...)
```

**Pros**:
- Works today
- No protocol changes

**Cons**:
- Loses semantic meaning (it's not really a string)
- Can't query individual keys in Terraform expressions
- Duplicates data
- Ugly workaround

### Option 2: Description/MarkdownDescription

Use description to explain the format:

```go
"annotations": schema.MapAttribute{
    ElementType: types.StringType,
    Computed: true,
    Description: "Note: Changes shown in compact format. Use 'terraform show' for details.",
}
```

**Pros**:
- No code changes needed

**Cons**:
- Doesn't actually change rendering
- Just sets expectations

### Option 3: Post-Plan Processing

Use `terraform plan -json` and post-process the output:

```bash
terraform plan -json | jq '.resource_changes[] | ...'
```

**Pros**:
- Total control over display

**Cons**:
- Users must use custom tooling
- Not part of normal workflow
- Loses colorization and formatting

### Option 4: Terraform Core Feature Request

File an issue/RFC with HashiCorp Terraform requesting:
- Configurable rendering modes in schema
- Provider hints for diff display
- Or: better default rendering for certain patterns

**Pros**:
- Proper solution if accepted
- Benefits all providers

**Cons**:
- Requires Core team buy-in
- Major protocol change
- Long timeline (years potentially)
- May be rejected

## Technical Feasibility Assessment

### Can It Be Done?
**Yes, but...**

Technically feasible paths:

1. **Protocol v7 with rendering hints** (Major undertaking)
   - Effort: 6-12 months
   - Requires: Core team, protocol team, framework team coordination
   - Backward compatibility: New protocol version, gradual adoption

2. **Schema extension fields** (Smaller change)
   - Add optional `rendering_mode` field to existing protocol
   - Core ignores if not understood (graceful degradation)
   - Effort: 2-4 months
   - Requires: Core team approval and implementation

3. **Provider-side pre-formatted strings** (Immediate)
   - Provider computes formatted string representation
   - Sends as separate attribute
   - Effort: Days
   - Requires: No Core changes

### Recommendation

**Short term**: Use provider-side formatting workarounds (Option 1)

**Medium term**: File detailed RFC with Terraform Core team proposing schema-level rendering hints

**Long term**: If accepted, implement in tfprotov7 with proper framework support

## References

### Code Locations

**terraform-plugin-go**:
- Protocol definition: `tfprotov6/internal/tfplugin6/tfplugin6.proto`
- DynamicValue encoding: `tfprotov6/dynamic_value.go`

**terraform-plugin-framework**:
- Schema attributes: `resource/schema/string_attribute.go`, `resource/schema/map_attribute.go`
- PlanModifiers: `resource/schema/planmodifier/string.go`
- Protocol conversion: `internal/toproto6/dynamic_value.go`

**terraform (core)**:
- Diff computation: `internal/command/jsonformat/differ/attribute.go`
- Type-specific renderers: `internal/command/jsonformat/computed/renderers/`
- Diff interface: `internal/command/jsonformat/computed/diff.go`

### Related Issues

- hashicorp/terraform-plugin-framework#921 - Plan output display issues
- hashicorp/terraform-plugin-framework#70 - Diff suppression
- hashicorp/terraform-plugin-framework#1006 - Sensitive rendering
- hashicorp/terraform#26187 - Concise diff renderer (PR)
- hashicorp/terraform#28947 - Sensitive value redaction

## Conclusion

**Custom diff rendering is currently impossible from terraform-plugin-framework** because:

1. Rendering happens entirely in Terraform Core
2. Framework only sends opaque binary data (DynamicValue)
3. No protocol fields exist for rendering hints
4. No extension points in framework for this purpose

**To make it possible would require**:

1. Protocol buffer schema changes (new fields)
2. Terraform Core diff renderer changes
3. Framework API additions
4. Coordination across all three components

**Realistic path forward**:

1. Document the limitation clearly
2. Provide workarounds for providers (formatted string attributes)
3. File an RFC with concrete proposal to Terraform Core team
4. If accepted, implement across protocol → core → framework
5. Expect 6-12+ month timeline minimum

The `Sensitive` attribute flag proves the pattern works (schema metadata affecting rendering), but extending it requires Core team engagement and protocol evolution.
