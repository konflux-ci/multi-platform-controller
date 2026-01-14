/*
Copyright 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// retryClient wraps calls to apiserver with a retry mechanism to retry requests when apiserver returns 500-level errors
type retryClient struct {
	inner   client.Client
	backoff wait.Backoff
}

// Create implements client.Client.
func (r *retryClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	return retry.OnError(r.backoff, errors.IsInternalError, func() error {
		return r.inner.Create(ctx, obj, opts...)
	})
}

// Delete implements client.Client.
func (r *retryClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	return retry.OnError(r.backoff, errors.IsInternalError, func() error {
		return r.inner.Delete(ctx, obj, opts...)
	})
}

// DeleteAllOf implements client.Client.
func (r *retryClient) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	return retry.OnError(r.backoff, errors.IsInternalError, func() error {
		return r.inner.DeleteAllOf(ctx, obj, opts...)
	})
}

// Get implements client.Client.
func (r *retryClient) Get(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
	return retry.OnError(r.backoff, errors.IsInternalError, func() error {
		return r.inner.Get(ctx, key, obj, opts...)
	})
}

// GroupVersionKindFor implements client.Client.
func (r *retryClient) GroupVersionKindFor(obj runtime.Object) (schema.GroupVersionKind, error) {
	// no wrapping, since this method does not appear to touch apiserver
	return r.inner.GroupVersionKindFor(obj)
}

// IsObjectNamespaced implements client.Client.
func (r *retryClient) IsObjectNamespaced(obj runtime.Object) (bool, error) {
	// no wrapping, since this method does not appear to touch apiserver
	return r.inner.IsObjectNamespaced(obj)
}

// List implements client.Client.
func (r *retryClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return retry.OnError(r.backoff, errors.IsInternalError, func() error {
		return r.inner.List(ctx, list, opts...)
	})
}

// Patch implements client.Client.
func (r *retryClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	return retry.OnError(r.backoff, errors.IsInternalError, func() error {
		return r.inner.Patch(ctx, obj, patch, opts...)
	})
}

// RESTMapper implements client.Client.
func (r *retryClient) RESTMapper() meta.RESTMapper {
	// no wrapping, since this method does not appear to touch apiserver
	return r.inner.RESTMapper()
}

// Scheme implements client.Client.
func (r *retryClient) Scheme() *runtime.Scheme {
	// no wrapping, since this method does not appear to touch apiserver
	return r.inner.Scheme()
}

// Status implements client.Client.
func (r *retryClient) Status() client.SubResourceWriter {
	return &retrySubResourceWriter{
		inner:   r.inner.Status(),
		backoff: r.backoff,
	}
}

// SubResource implements client.Client.
func (r *retryClient) SubResource(subResource string) client.SubResourceClient {
	return &retrySubresourceClient{
		inner:   r.inner.SubResource(subResource),
		backoff: r.backoff,
	}
}

// Update implements client.Client.
func (r *retryClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	return retry.OnError(r.backoff, errors.IsInternalError, func() error {
		return r.inner.Update(ctx, obj, opts...)
	})
}

var _ client.Client = &retryClient{}

func NewRetryClient(client client.Client, backoff wait.Backoff) client.Client {
	return &retryClient{
		inner:   client,
		backoff: backoff,
	}
}
