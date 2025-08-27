// main.go
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
)

func main() {
	var debug bool

	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	opts := providerserver.ServeOpts{
		Address: "registry.terraform.io/jmorris0x0/k8sconnect",
		Debug:   debug,
	}

	err := providerserver.Serve(context.Background(), k8sconnect.New, opts)

	if err != nil {
		log.Fatal(err.Error())
	}
}
