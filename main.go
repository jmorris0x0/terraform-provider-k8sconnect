package main

import (
	"context"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/jmorris0x0/terraform-provider-k8sinline/k8sinline"
)

func main() {
	providerserver.Serve(context.Background(), k8sinline.New, providerserver.ServeOpts{
		Address: "registry.terraform.io/jmorris0x0/k8sinline",
	})
}
