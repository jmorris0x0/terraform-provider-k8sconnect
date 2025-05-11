// main.go
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline"
)

func main() {
	var debug bool

	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	opts := providerserver.ServeOpts{
		Address: "registry.terraform.io/jmorris0x0/k8sinline",
		Debug:   debug,
	}

	err := providerserver.Serve(context.Background(), k8sinline.New, opts)

	if err != nil {
		log.Fatal(err.Error())
	}
}
