package client

import (
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	restclient "k8s.io/client-go/rest"
)

type Client struct {
	restMapper meta.RESTMapper
	client     dynamic.Interface
}

func New(config *restclient.Config, restMapper meta.RESTMapper) (*Client, error) {
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get create dynamic client")
	}
	return &Client{
		restMapper: restMapper,
		client:     dynamicClient,
	}, nil
}

func (c *Client) GetResourceInterface(gvk schema.GroupVersionKind, ns string) (dynamic.ResourceInterface, error) {
	mapping, err := c.restMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get rest mapping")
	}
	if mapping.Scope.Name() == meta.RESTScopeNameRoot {
		return c.client.Resource(mapping.Resource), nil
	}
	return c.client.Resource(mapping.Resource).Namespace(ns), nil
}
