package testing_utils

import (
	"context"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// ErrorClient is a wrapper around fake.Client that allows you to test scenarios where a function will do something with
// a k82 controller-runtime that might return an error, and generating such an error is technically difficult or
// time-costly
type ErrorClient struct {
	client.Client
	errToReturn error
}

// NewErrorClient creates a new ErrorClient with the specified scheme and error.
func NewErrorClient(scheme *runtime.Scheme, errToReturn error) *ErrorClient {
	return &ErrorClient{
		Client:      fake.NewClientBuilder().WithScheme(scheme).Build(),
		errToReturn: errToReturn,
	}
}

// List returns the specified error.
func (c *ErrorClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return c.errToReturn
}
