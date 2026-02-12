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
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type retrySubResourceWriter struct {
	inner   client.SubResourceWriter
	backoff wait.Backoff
}

// Create implements client.SubResourceWriter.
func (r *retrySubResourceWriter) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	return retry.OnError(r.backoff, errors.IsInternalError, func() error {
		return r.inner.Create(ctx, obj, subResource, opts...)
	})
}

// Patch implements client.SubResourceWriter.
func (r *retrySubResourceWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	return retry.OnError(r.backoff, errors.IsInternalError, func() error {
		return r.inner.Patch(ctx, obj, patch, opts...)
	})
}

// Update implements client.SubResourceWriter.
func (r *retrySubResourceWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return retry.OnError(r.backoff, errors.IsInternalError, func() error {
		return r.inner.Update(ctx, obj, opts...)
	})
}

var _ client.SubResourceWriter = &retrySubResourceWriter{}
