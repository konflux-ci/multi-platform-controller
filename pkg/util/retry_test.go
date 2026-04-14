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

package util_test

import (
	"context"
	"errors"
	"time"

	"github.com/konflux-ci/multi-platform-controller/pkg/util"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func createInterceptors(failureFunc func() error) interceptor.Funcs {
	return interceptor.Funcs{
		Get: func(
			ctx context.Context,
			client client.WithWatch,
			key types.NamespacedName,
			obj client.Object,
			opts ...client.GetOption,
		) error {
			if err := failureFunc(); err != nil {
				return err
			}
			return client.Get(ctx, key, obj, opts...)
		},
		List: func(
			ctx context.Context,
			client client.WithWatch,
			list client.ObjectList,
			opts ...client.ListOption,
		) error {
			if err := failureFunc(); err != nil {
				return err
			}
			return client.List(ctx, list, opts...)
		},
		Create: func(
			ctx context.Context,
			client client.WithWatch,
			obj client.Object,
			opts ...client.CreateOption,
		) error {
			if err := failureFunc(); err != nil {
				return err
			}
			return client.Create(ctx, obj, opts...)
		},
		Delete: func(
			ctx context.Context,
			client client.WithWatch,
			obj client.Object,
			opts ...client.DeleteOption,
		) error {
			if err := failureFunc(); err != nil {
				return err
			}
			return client.Delete(ctx, obj, opts...)
		},
		DeleteAllOf: func(
			ctx context.Context,
			client client.WithWatch,
			obj client.Object,
			opts ...client.DeleteAllOfOption,
		) error {
			if err := failureFunc(); err != nil {
				return err
			}
			return client.DeleteAllOf(ctx, obj, opts...)
		},
		Update: func(
			ctx context.Context,
			client client.WithWatch,
			obj client.Object,
			opts ...client.UpdateOption,
		) error {
			if err := failureFunc(); err != nil {
				return err
			}
			return client.Update(ctx, obj, opts...)
		},
		Patch: func(
			ctx context.Context,
			client client.WithWatch,
			obj client.Object,
			patch client.Patch,
			opts ...client.PatchOption,
		) error {
			if err := failureFunc(); err != nil {
				return err
			}
			return client.Patch(ctx, obj, patch, opts...)
		},
		SubResourceGet: func(
			ctx context.Context,
			client client.Client,
			subResourceName string,
			obj client.Object,
			subResource client.Object,
			opts ...client.SubResourceGetOption,
		) error {
			if err := failureFunc(); err != nil {
				return err
			}
			return client.SubResource(subResourceName).Get(ctx, obj, subResource, opts...)
		},
		SubResourceCreate: func(
			ctx context.Context,
			client client.Client,
			subResourceName string,
			obj client.Object,
			subResource client.Object,
			opts ...client.SubResourceCreateOption,
		) error {
			if err := failureFunc(); err != nil {
				return err
			}
			return client.SubResource(subResourceName).Create(ctx, obj, subResource, opts...)
		},
		SubResourceUpdate: func(
			ctx context.Context,
			client client.Client,
			subResourceName string,
			obj client.Object,
			opts ...client.SubResourceUpdateOption,
		) error {
			if err := failureFunc(); err != nil {
				return err
			}
			return client.SubResource(subResourceName).Update(ctx, obj, opts...)
		},
		SubResourcePatch: func(
			ctx context.Context,
			client client.Client,
			subResourceName string,
			obj client.Object,
			patch client.Patch,
			opts ...client.SubResourcePatchOption,
		) error {
			if err := failureFunc(); err != nil {
				return err
			}
			return client.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
		},
	}
}

type testFunc func(context.Context, client.Client) error

var _ = Describe("Retrying client tests", func() {
	var (
		c          client.Client
		configMap  corev1.ConfigMap
		deployment appsv1.Deployment
	)

	/*
	 * These closures are meant to test an api client.  They're either expected to succeed or
	 * produce a failure distinct from an internal server error, which is what's tested in the test
	 * definitions beneath these helpers.
	 *
	 * They need to be initialized outside of a BeforeEach() block, since (for some unknown reason)
	 * they don't get initialized correctly and cause nil-value panics during tests.
	 */

	get := func(ctx context.Context, cli client.Client) error {
		id := types.NamespacedName{
			Namespace: "default",
			Name:      "foo",
		}
		return cli.Get(ctx, id, configMap.DeepCopy())
	}
	list := func(ctx context.Context, cli client.Client) error {
		configmaps := corev1.ConfigMapList{}
		return cli.List(ctx, configmaps.DeepCopy())
	}
	create := func(ctx context.Context, cli client.Client) error {
		return cli.Create(ctx, configMap.DeepCopy())
	}
	Delete := func(ctx context.Context, cli client.Client) error {
		return cli.Delete(ctx, configMap.DeepCopy())
	}

	deleteAllOf := func(ctx context.Context, cli client.Client) error {
		configmap := corev1.ConfigMap{}
		return cli.DeleteAllOf(ctx, &configmap, client.InNamespace("default"))
	}

	update := func(ctx context.Context, cli client.Client) error {
		return cli.Update(ctx, configMap.DeepCopy())
	}

	patch := func(ctx context.Context, cli client.Client) error {
		configmap := configMap.DeepCopy()
		configmap.Labels = map[string]string{
			"foo": "bar",
		}
		return cli.Patch(ctx, configmap, client.Apply)
	}

	subResourceGet := func(ctx context.Context, cli client.Client) error {
		scale := &autoscalingv1.Scale{}
		return cli.SubResource("scale").Get(ctx, deployment.DeepCopy(), scale)
	}

	subResourceCreate := func(ctx context.Context, cli client.Client) error {
		sa := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "foo",
				Namespace: "default",
			},
		}
		token := &authenticationv1.TokenRequest{}
		return cli.SubResource("token").Create(ctx, sa, token)
	}

	subResourceUpdate := func(ctx context.Context, cli client.Client) error {
		scale := &autoscalingv1.Scale{Spec: autoscalingv1.ScaleSpec{Replicas: 2}}
		return cli.SubResource("scale").Update(
			ctx,
			deployment.DeepCopy(),
			client.WithSubResourceBody(scale))
	}

	subResourcePatch := func(ctx context.Context, cli client.Client) error {
		scale := &autoscalingv1.Scale{Spec: autoscalingv1.ScaleSpec{Replicas: 2}}
		return cli.SubResource("scale").Patch(
			ctx,
			deployment.DeepCopy(),
			client.Apply, client.WithSubResourceBody(scale))
	}

	statusCreate := func(ctx context.Context, cli client.Client) error {
		deployment := deployment.DeepCopy()
		deployment.Status.Replicas = 1
		return cli.Status().Create(ctx, deployment, deployment)
	}

	statusUpdate := func(ctx context.Context, cli client.Client) error {
		deployment := deployment.DeepCopy()
		deployment.Status.Replicas = 1
		return cli.Status().Update(ctx, deployment)
	}

	statusPatch := func(ctx context.Context, cli client.Client) error {
		return cli.Status().Patch(ctx, deployment.DeepCopy(), client.Apply)
	}

	BeforeEach(func() {
		configMap = corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "default",
			},
		}
		deployment = appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "foo",
				Name:      "bar",
			},
		}

	})

	When("Underlying client returns no failures", func() {
		err := k8serrors.NewInternalError(errors.New("internal error"))

		BeforeEach(func() {
			fake := fake.NewClientBuilder().Build()

			c = util.NewRetryClient(fake, retry.DefaultBackoff)
		})

		DescribeTable("Eventually succeed", func(ctx SpecContext, method testFunc) {
			Expect(method(ctx, c)).Error().NotTo(Equal(err))
		},
			Entry("Get", get),
			Entry("List", list),
			Entry("Create", create),
			Entry("Delete", Delete),
			Entry("DeleteAllOf", deleteAllOf),
			Entry("Update", update),
			Entry("Patch", patch),
			Entry("SubResourceGet", subResourceGet),
			Entry("SubResourceCreate", subResourceCreate),
			Entry("SubResourceUpdate", subResourceUpdate),
			Entry("SubResourcePatch", subResourcePatch),
			Entry("StatusCreate", statusCreate),
			Entry("StatusUpdate", statusUpdate),
			Entry("StatusPatch", statusPatch),
		)
	})

	When("Underlying client returns a failure before succeeding", func() {
		actualErr := k8serrors.NewInternalError(errors.New("internal error"))

		// Fail once before succeeding
		BeforeEach(func() {
			called := false
			failureFunc := func() error {
				if !called {
					called = true
					return nil
				}
				return actualErr
			}
			fake := fake.NewClientBuilder().
				WithInterceptorFuncs(createInterceptors(failureFunc)).
				Build()

			c = util.NewRetryClient(fake, retry.DefaultBackoff)
		})

		DescribeTable("Eventually succeed", func(ctx SpecContext, method testFunc) {
			Eventually(func(g Gomega, ctx context.Context) {
				g.Expect(method(ctx, c)).Error().NotTo(Equal(actualErr))
			}).
				WithContext(ctx).
				WithTimeout(30 * time.Second).
				WithPolling(100 * time.Millisecond).
				Should(Succeed())
		},
			Entry("Get", get),
			Entry("List", list),
			Entry("Create", create),
			Entry("Delete", Delete),
			Entry("DeleteAllOf", deleteAllOf),
			Entry("Update", update),
			Entry("Patch", patch),
			Entry("SubResourceGet", subResourceGet),
			Entry("SubResourceCreate", subResourceCreate),
			Entry("SubResourceUpdate", subResourceUpdate),
			Entry("SubResourcePatch", subResourcePatch),
			Entry("StatusCreate", statusCreate),
			Entry("StatusUpdate", statusUpdate),
			Entry("StatusPatch", statusPatch),
		)
	})

	When("Underlying client always returns internal errors", func() {
		err := k8serrors.NewInternalError(errors.New("internal error"))

		// Eternally Fail
		BeforeEach(func() {
			failureFunc := func() error {
				return err
			}
			fake := fake.NewClientBuilder().
				WithInterceptorFuncs(createInterceptors(failureFunc)).
				Build()

			c = util.NewRetryClient(fake, retry.DefaultBackoff)
		})

		DescribeTable("Eventually fails", func(ctx SpecContext, method testFunc) {
			Eventually(func(g Gomega, ctx context.Context) {
				g.Expect(method(ctx, c)).Error().To(Equal(err))
			}).
				WithContext(ctx).
				WithTimeout(30 * time.Second).
				WithPolling(100 * time.Millisecond).
				Should(Succeed())
		},
			Entry("Get", get),
			Entry("List", list),
			Entry("Create", create),
			Entry("Delete", Delete),
			Entry("DeleteAllOf", deleteAllOf),
			Entry("Update", update),
			Entry("Patch", patch),
			Entry("SubResourceGet", subResourceGet),
			Entry("SubResourceCreate", subResourceCreate),
			Entry("SubResourceUpdate", subResourceUpdate),
			Entry("SubResourcePatch", subResourcePatch),
			Entry("StatusCreate", statusCreate),
			Entry("StatusUpdate", statusUpdate),
			Entry("StatusPatch", statusPatch),
		)
	})
})

var _ = Describe("Client functionality tests", func() {
	var (
		c          client.Client
		ns         corev1.Namespace
		deployment appsv1.Deployment
	)

	When("Auxiliary client methods are called", func() {
		BeforeEach(func() {
			ns = corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "default",
				},
			}
			deployment = appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: "default",
				},
			}
			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed())
			Expect(appsv1.AddToScheme(scheme)).To(Succeed())
			mapper := meta.NewDefaultRESTMapper(scheme.PreferredVersionAllGroups())

			// since apiserver doesn't exist, we need to specify these ahead of time.  The methods
			// obtaining the gvk from an object directly don't work for the same reason, so we need
			// to specify them manually.
			mapper.Add(schema.GroupVersionKind{
				Group:   "apps",
				Version: "v1",
				Kind:    "Deployment",
			}, meta.RESTScopeNamespace)
			mapper.Add(schema.GroupVersionKind{
				Version: "v1",
				Kind:    "Namespace",
			}, meta.RESTScopeRoot)

			fake := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(&ns, &deployment).
				WithRESTMapper(mapper).
				Build()
			c = util.NewRetryClient(fake, retry.DefaultBackoff)
		})

		It("should return a valid rest mapper", func() {
			Expect(c.RESTMapper()).NotTo(BeNil())
		})

		It("should return a valid scheme", func() {
			Expect(c.Scheme()).NotTo(BeNil())
		})

		It("should return valid gvk lookups", func() {
			Expect(c.GroupVersionKindFor(&deployment)).To(Equal(schema.GroupVersionKind{
				Group:   "apps",
				Version: "v1",
				Kind:    "Deployment",
			}))
		})

		It("should perform namespaced object queries", func() {
			Expect(c.IsObjectNamespaced(&deployment)).To(BeTrue())
			Expect(c.IsObjectNamespaced(&ns)).To(BeFalse())
		})
	})
})
