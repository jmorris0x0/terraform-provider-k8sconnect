package k8sinline

import (
	"context"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

type provider struct{}

func New() provider.Provider { return &provider{} }

func (p *provider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "k8sinline"
	resp.Version = "0.1.0"
}

func (p *provider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{} // no top‑level config
}

func (p *provider) Configure(context.Context, provider.ConfigureRequest, *provider.ConfigureResponse) {
}

func (p *provider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{NewManifestResource}
}

func (p *provider) DataSources(_ context.Context) []func() resource.DataSource {
	return nil
}
