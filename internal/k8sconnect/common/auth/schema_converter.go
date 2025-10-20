package auth

import (
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
)

// ConvertResourceAttributesToDatasource converts resource schema attributes to datasource schema attributes.
// This handles the type incompatibility between resource and datasource schemas in the Terraform framework.
func ConvertResourceAttributesToDatasource(resourceAttrs map[string]resourceschema.Attribute) map[string]datasourceschema.Attribute {
	datasourceAttrs := make(map[string]datasourceschema.Attribute)

	for key, attr := range resourceAttrs {
		datasourceAttrs[key] = convertSingleAttribute(attr)
	}

	return datasourceAttrs
}

// convertSingleAttribute converts a single resource attribute to a datasource attribute
func convertSingleAttribute(resourceAttr resourceschema.Attribute) datasourceschema.Attribute {
	switch attr := resourceAttr.(type) {
	case resourceschema.StringAttribute:
		return datasourceschema.StringAttribute{
			Required:            attr.Required,
			Optional:            attr.Optional,
			Computed:            attr.Computed,
			Sensitive:           attr.Sensitive,
			Description:         attr.Description,
			MarkdownDescription: attr.MarkdownDescription,
			DeprecationMessage:  attr.DeprecationMessage,
			// Validators are not converted as they're type-specific
		}

	case resourceschema.BoolAttribute:
		return datasourceschema.BoolAttribute{
			Required:            attr.Required,
			Optional:            attr.Optional,
			Computed:            attr.Computed,
			Sensitive:           attr.Sensitive,
			Description:         attr.Description,
			MarkdownDescription: attr.MarkdownDescription,
			DeprecationMessage:  attr.DeprecationMessage,
		}

	case resourceschema.Int64Attribute:
		return datasourceschema.Int64Attribute{
			Required:            attr.Required,
			Optional:            attr.Optional,
			Computed:            attr.Computed,
			Sensitive:           attr.Sensitive,
			Description:         attr.Description,
			MarkdownDescription: attr.MarkdownDescription,
			DeprecationMessage:  attr.DeprecationMessage,
		}

	case resourceschema.Float64Attribute:
		return datasourceschema.Float64Attribute{
			Required:            attr.Required,
			Optional:            attr.Optional,
			Computed:            attr.Computed,
			Sensitive:           attr.Sensitive,
			Description:         attr.Description,
			MarkdownDescription: attr.MarkdownDescription,
			DeprecationMessage:  attr.DeprecationMessage,
		}

	case resourceschema.ListAttribute:
		return datasourceschema.ListAttribute{
			Required:            attr.Required,
			Optional:            attr.Optional,
			Computed:            attr.Computed,
			Sensitive:           attr.Sensitive,
			Description:         attr.Description,
			MarkdownDescription: attr.MarkdownDescription,
			DeprecationMessage:  attr.DeprecationMessage,
			ElementType:         attr.ElementType,
		}

	case resourceschema.MapAttribute:
		return datasourceschema.MapAttribute{
			Required:            attr.Required,
			Optional:            attr.Optional,
			Computed:            attr.Computed,
			Sensitive:           attr.Sensitive,
			Description:         attr.Description,
			MarkdownDescription: attr.MarkdownDescription,
			DeprecationMessage:  attr.DeprecationMessage,
			ElementType:         attr.ElementType,
		}

	case resourceschema.SetAttribute:
		return datasourceschema.SetAttribute{
			Required:            attr.Required,
			Optional:            attr.Optional,
			Computed:            attr.Computed,
			Sensitive:           attr.Sensitive,
			Description:         attr.Description,
			MarkdownDescription: attr.MarkdownDescription,
			DeprecationMessage:  attr.DeprecationMessage,
			ElementType:         attr.ElementType,
		}

	case resourceschema.ObjectAttribute:
		return datasourceschema.ObjectAttribute{
			Required:            attr.Required,
			Optional:            attr.Optional,
			Computed:            attr.Computed,
			Sensitive:           attr.Sensitive,
			Description:         attr.Description,
			MarkdownDescription: attr.MarkdownDescription,
			DeprecationMessage:  attr.DeprecationMessage,
			AttributeTypes:      attr.AttributeTypes,
		}

	case resourceschema.SingleNestedAttribute:
		// Recursively convert nested attributes
		return datasourceschema.SingleNestedAttribute{
			Required:            attr.Required,
			Optional:            attr.Optional,
			Computed:            attr.Computed,
			Sensitive:           attr.Sensitive,
			Description:         attr.Description,
			MarkdownDescription: attr.MarkdownDescription,
			DeprecationMessage:  attr.DeprecationMessage,
			Attributes:          ConvertResourceAttributesToDatasource(attr.Attributes),
		}

	case resourceschema.ListNestedAttribute:
		// Recursively convert nested attributes
		return datasourceschema.ListNestedAttribute{
			Required:            attr.Required,
			Optional:            attr.Optional,
			Computed:            attr.Computed,
			Sensitive:           attr.Sensitive,
			Description:         attr.Description,
			MarkdownDescription: attr.MarkdownDescription,
			DeprecationMessage:  attr.DeprecationMessage,
			NestedObject: datasourceschema.NestedAttributeObject{
				Attributes: ConvertResourceAttributesToDatasource(attr.NestedObject.Attributes),
			},
		}

	case resourceschema.SetNestedAttribute:
		// Recursively convert nested attributes
		return datasourceschema.SetNestedAttribute{
			Required:            attr.Required,
			Optional:            attr.Optional,
			Computed:            attr.Computed,
			Sensitive:           attr.Sensitive,
			Description:         attr.Description,
			MarkdownDescription: attr.MarkdownDescription,
			DeprecationMessage:  attr.DeprecationMessage,
			NestedObject: datasourceschema.NestedAttributeObject{
				Attributes: ConvertResourceAttributesToDatasource(attr.NestedObject.Attributes),
			},
		}

	case resourceschema.MapNestedAttribute:
		// Recursively convert nested attributes
		return datasourceschema.MapNestedAttribute{
			Required:            attr.Required,
			Optional:            attr.Optional,
			Computed:            attr.Computed,
			Sensitive:           attr.Sensitive,
			Description:         attr.Description,
			MarkdownDescription: attr.MarkdownDescription,
			DeprecationMessage:  attr.DeprecationMessage,
			NestedObject: datasourceschema.NestedAttributeObject{
				Attributes: ConvertResourceAttributesToDatasource(attr.NestedObject.Attributes),
			},
		}

	case resourceschema.DynamicAttribute:
		return datasourceschema.DynamicAttribute{
			Required:            attr.Required,
			Optional:            attr.Optional,
			Computed:            attr.Computed,
			Sensitive:           attr.Sensitive,
			Description:         attr.Description,
			MarkdownDescription: attr.MarkdownDescription,
			DeprecationMessage:  attr.DeprecationMessage,
		}

	default:
		// This shouldn't happen with standard framework attributes
		// but return a computed string as a fallback
		return datasourceschema.StringAttribute{
			Computed:    true,
			Description: "Unknown attribute type - defaulted to computed string",
		}
	}
}
