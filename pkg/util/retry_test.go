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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

/*
These functions are meant to test an api client.  They're either expected to succeed or produce a
failure distinct from an internal server error, which is what's tested in the test definitions
beneath these helpers.
*/

func Get(ctx context.Context, c client.Client) error {
	configmap := corev1.ConfigMap{}
	id := types.NamespacedName{
		Namespace: "default",
		Name:      "foo",
	}
	return c.Get(ctx, id, &configmap)
}

func List(ctx context.Context, c client.Client) error {
	configmaps := corev1.ConfigMapList{}
	return c.List(ctx, &configmaps)
}

func Create(ctx context.Context, c client.Client) error {
	configmap := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}
	return c.Create(ctx, &configmap)
}

func Delete(ctx context.Context, c client.Client) error {
	configmap := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}
	return c.Delete(ctx, &configmap)
}

func DeleteAllOf(ctx context.Context, c client.Client) error {
	configmap := corev1.ConfigMap{}
	return c.DeleteAllOf(ctx, &configmap, client.InNamespace("default"))
}

func Update(ctx context.Context, c client.Client) error {
	configmap := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
			Labels: map[string]string{
				"foo": "bar",
			},
		},
	}
	return c.Update(ctx, &configmap)
}

func Patch(ctx context.Context, c client.Client) error {
	configmap := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
			Labels: map[string]string{
				"foo": "bar",
			},
		},
	}
	return c.Patch(ctx, &configmap, client.Apply)
}

func SubResourceGet(ctx context.Context, c client.Client) error {
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "foo", Name: "bar"}}
	scale := &autoscalingv1.Scale{}
	return c.SubResource("scale").Get(ctx, dep, scale)
}

func SubResourceCreate(ctx context.Context, c client.Client) error {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "foo",
			Namespace: "default",
		},
	}
	token := &authenticationv1.TokenRequest{}
	return c.SubResource("token").Create(ctx, sa, token)
}

func SubResourceUpdate(ctx context.Context, c client.Client) error {
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "foo", Name: "bar"}}
	scale := &autoscalingv1.Scale{Spec: autoscalingv1.ScaleSpec{Replicas: 2}}
	return c.SubResource("scale").Update(ctx, dep, client.WithSubResourceBody(scale))
}

func SubResourcePatch(ctx context.Context, c client.Client) error {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "foo",
			Name:      "bar",
		},
		Status: appsv1.DeploymentStatus{
			Replicas: 1,
		},
	}
	scale := &autoscalingv1.Scale{Spec: autoscalingv1.ScaleSpec{Replicas: 2}}
	return c.SubResource("scale").Patch(ctx, dep, client.Apply, client.WithSubResourceBody(scale))
}

func StatusCreate(ctx context.Context, c client.Client) error {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "foo",
			Name:      "bar",
		},
		Status: appsv1.DeploymentStatus{
			Replicas: 1,
		},
	}
	return c.Status().Create(ctx, dep, dep)
}

func StatusUpdate(ctx context.Context, c client.Client) error {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "foo",
			Name:      "bar",
		},
		Status: appsv1.DeploymentStatus{
			Replicas: 1,
		},
	}
	return c.Status().Update(ctx, dep)
}

func StatusPatch(ctx context.Context, c client.Client) error {
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "foo", Name: "bar"}}
	return c.Status().Patch(ctx, dep, client.Apply)
}

var _ = Describe("Retrying client tests", func() {
	var c client.Client

	When("Underlying client returns no failures", func() {
		err := k8serrors.NewInternalError(errors.New("internal error"))

		BeforeEach(func() {
			fake := fake.NewClientBuilder().Build()

			c = util.NewRetryClient(fake, retry.DefaultBackoff)
		})

		DescribeTable("Eventually succeed", func(ctx SpecContext, method func(context.Context, client.Client) error) {
			Expect(method(ctx, c)).Error().NotTo(Equal(err))
		},
			Entry("Get", Get),
			Entry("List", List),
			Entry("Create", Create),
			Entry("Delete", Delete),
			Entry("DeleteAllOf", DeleteAllOf),
			Entry("Update", Update),
			Entry("Patch", Patch),
			Entry("SubResourceGet", SubResourceGet),
			Entry("SubResourceCreate", SubResourceCreate),
			Entry("SubResourceUpdate", SubResourceUpdate),
			Entry("SubResourcePatch", SubResourcePatch),
			Entry("StatusCreate", StatusCreate),
			Entry("StatusUpdate", StatusUpdate),
			Entry("StatusPatch", StatusPatch),
		)
	})

	When("Underlying client returns a failure before succeeding", func() {
		actualErr := k8serrors.NewInternalError(errors.New("internal error"))

		// Fail once before succeeding
		BeforeEach(func() {
			err := k8serrors.NewInternalError(errors.New("internal error"))
			fake := fake.NewClientBuilder().
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, client client.WithWatch, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
						if err != nil {
							defer func() {
								err = nil
							}()
							return err
						} else {
							return client.Get(ctx, key, obj, opts...)
						}
					},
					List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
						if err != nil {
							defer func() {
								err = nil
							}()
							return err
						} else {
							return client.List(ctx, list, opts...)
						}
					},
					Create: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
						if err != nil {
							defer func() {
								err = nil
							}()
							return err
						} else {
							return client.Create(ctx, obj, opts...)
						}
					},
					Delete: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
						if err != nil {
							defer func() {
								err = nil
							}()
							return err
						} else {
							return client.Delete(ctx, obj, opts...)
						}
					},
					DeleteAllOf: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.DeleteAllOfOption) error {
						if err != nil {
							defer func() {
								err = nil
							}()
							return err
						} else {
							return client.DeleteAllOf(ctx, obj, opts...)
						}
					},
					Update: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
						if err != nil {
							defer func() {
								err = nil
							}()
							return err
						} else {
							return client.Update(ctx, obj, opts...)
						}
					},
					Patch: func(ctx context.Context, client client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						if err != nil {
							defer func() {
								err = nil
							}()
							return err
						} else {
							return client.Patch(ctx, obj, patch, opts...)
						}
					},
					SubResourceGet: func(ctx context.Context, client client.Client, subResourceName string, obj client.Object, subResource client.Object, opts ...client.SubResourceGetOption) error {
						if err != nil {
							defer func() {
								err = nil
							}()
							return err
						} else {
							return client.SubResource(subResourceName).Get(ctx, obj, subResource, opts...)
						}
					},
					SubResourceCreate: func(ctx context.Context, client client.Client, subResourceName string, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
						if err != nil {
							defer func() {
								err = nil
							}()
							return err
						} else {
							return client.SubResource(subResourceName).Create(ctx, obj, subResource, opts...)
						}
					},
					SubResourceUpdate: func(ctx context.Context, client client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
						if err != nil {
							defer func() {
								err = nil
							}()
							return err
						} else {
							return client.SubResource(subResourceName).Update(ctx, obj, opts...)
						}
					},
					SubResourcePatch: func(ctx context.Context, client client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
						if err != nil {
							defer func() {
								err = nil
							}()
							return err
						} else {
							return client.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
						}
					},
				}).
				Build()

			c = util.NewRetryClient(fake, retry.DefaultBackoff)
		})

		DescribeTable("Eventually succeed", func(ctx SpecContext, method func(context.Context, client.Client) error) {
			Eventually(func(g Gomega, ctx context.Context) {
				g.Expect(method(ctx, c)).Error().NotTo(Equal(actualErr))
			}).WithContext(ctx).WithTimeout(30 * time.Second).WithPolling(100 * time.Millisecond).Should(Succeed())
		},
			Entry("Get", Get),
			Entry("List", List),
			Entry("Create", Create),
			Entry("Delete", Delete),
			Entry("DeleteAllOf", DeleteAllOf),
			Entry("Update", Update),
			Entry("Patch", Patch),
			Entry("SubResourceGet", SubResourceGet),
			Entry("SubResourceCreate", SubResourceCreate),
			Entry("SubResourceUpdate", SubResourceUpdate),
			Entry("SubResourcePatch", SubResourcePatch),
			Entry("StatusCreate", StatusCreate),
			Entry("StatusUpdate", StatusUpdate),
			Entry("StatusPatch", StatusPatch),
		)
	})

	When("Underlying client always returns internal errors", func() {
		err := k8serrors.NewInternalError(errors.New("internal error"))

		// Eternally Fail
		BeforeEach(func() {
			fake := fake.NewClientBuilder().
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, client client.WithWatch, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
						return err
					},
					List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
						return err
					},
					Create: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
						return err
					},
					Delete: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
						return err
					},
					DeleteAllOf: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.DeleteAllOfOption) error {
						return err
					},
					Update: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
						return err
					},
					Patch: func(ctx context.Context, client client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						return err
					},
					SubResourceGet: func(ctx context.Context, client client.Client, subResourceName string, obj client.Object, subResource client.Object, opts ...client.SubResourceGetOption) error {
						return err
					},
					SubResourceCreate: func(ctx context.Context, client client.Client, subResourceName string, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
						return err
					},
					SubResourceUpdate: func(ctx context.Context, client client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
						return err
					},
					SubResourcePatch: func(ctx context.Context, client client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
						return err
					},
				}).
				Build()

			c = util.NewRetryClient(fake, retry.DefaultBackoff)
		})

		DescribeTable("Eventually fails", func(ctx SpecContext, method func(context.Context, client.Client) error) {
			Eventually(func(g Gomega, ctx context.Context) {
				g.Expect(method(ctx, c)).Error().To(Equal(err))
			}).WithContext(ctx).WithTimeout(30 * time.Second).WithPolling(100 * time.Millisecond).Should(Succeed())
		},
			Entry("Get", Get),
			Entry("List", List),
			Entry("Create", Create),
			Entry("Delete", Delete),
			Entry("DeleteAllOf", DeleteAllOf),
			Entry("Update", Update),
			Entry("Patch", Patch),
			Entry("SubResourceGet", SubResourceGet),
			Entry("SubResourceCreate", SubResourceCreate),
			Entry("SubResourceUpdate", SubResourceUpdate),
			Entry("SubResourcePatch", SubResourcePatch),
			Entry("StatusCreate", StatusCreate),
			Entry("StatusUpdate", StatusUpdate),
			Entry("StatusPatch", StatusPatch),
		)
	})
})
