package helm_release

import (
	"context"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/factory"
)

// restClientGetter implements genericclioptions.RESTClientGetter for Helm
// It bridges our k8sconnect auth/client system to Helm's expectations
type restClientGetter struct {
	namespace     string
	clusterModel  auth.ClusterModel
	clientFactory factory.ClientFactory
	restConfig    *rest.Config
}

// newRESTClientGetter creates a RESTClientGetter from our cluster configuration
func newRESTClientGetter(ctx context.Context, namespace string, clusterModel auth.ClusterModel, clientFactory factory.ClientFactory) (*restClientGetter, error) {
	// Create rest.Config from cluster model using our existing auth infrastructure
	restConfig, err := auth.CreateRESTConfig(ctx, clusterModel)
	if err != nil {
		return nil, err
	}

	return &restClientGetter{
		namespace:     namespace,
		clusterModel:  clusterModel,
		clientFactory: clientFactory,
		restConfig:    restConfig,
	}, nil
}

// ToRESTConfig returns the REST config
func (r *restClientGetter) ToRESTConfig() (*rest.Config, error) {
	return r.restConfig, nil
}

// ToDiscoveryClient returns a discovery client
func (r *restClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(r.restConfig)
	if err != nil {
		return nil, err
	}
	return memory.NewMemCacheClient(discoveryClient), nil
}

// ToRESTMapper returns a REST mapper
func (r *restClientGetter) ToRESTMapper() (meta.RESTMapper, error) {
	discoveryClient, err := r.ToDiscoveryClient()
	if err != nil {
		return nil, err
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(discoveryClient)
	return mapper, nil
}

// ToRawKubeConfigLoader returns a client config loader
// This is rarely used by Helm but required by the interface
func (r *restClientGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	// Return a simple implementation that just returns our config
	return &simpleClientConfig{
		restConfig: r.restConfig,
		namespace:  r.namespace,
	}
}

// simpleClientConfig implements clientcmd.ClientConfig
type simpleClientConfig struct {
	restConfig *rest.Config
	namespace  string
}

func (c *simpleClientConfig) RawConfig() (clientcmdapi.Config, error) {
	// Return empty kubeconfig - we don't use file-based config
	return clientcmdapi.Config{}, nil
}

func (c *simpleClientConfig) ClientConfig() (*rest.Config, error) {
	return c.restConfig, nil
}

func (c *simpleClientConfig) Namespace() (string, bool, error) {
	if c.namespace != "" {
		return c.namespace, false, nil
	}
	return "default", false, nil
}

func (c *simpleClientConfig) ConfigAccess() clientcmd.ConfigAccess {
	return nil
}

// Verify we implement the interface
var _ genericclioptions.RESTClientGetter = (*restClientGetter)(nil)
var _ clientcmd.ClientConfig = (*simpleClientConfig)(nil)
