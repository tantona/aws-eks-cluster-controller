// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package statefulset

import (
	"testing"
	"time"

	clusterv1alpha1 "github.com/awslabs/aws-eks-cluster-controller/pkg/apis/cluster/v1alpha1"
	componentsv1alpha1 "github.com/awslabs/aws-eks-cluster-controller/pkg/apis/components/v1alpha1"
	"github.com/awslabs/aws-eks-cluster-controller/pkg/authorizer"
	"github.com/awslabs/aws-eks-cluster-controller/pkg/logging"
	"github.com/onsi/gomega"
	"golang.org/x/net/context"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var c client.Client

var expectedRequest = reconcile.Request{NamespacedName: types.NamespacedName{Name: "foo-statefulset", Namespace: "default"}}
var sfsKey = types.NamespacedName{Name: "foo-statefulset", Namespace: "default"}
var rSfsKey = types.NamespacedName{Name: "remote-foo-statefulset", Namespace: "default"}

const timeout = time.Second * 10

// This is for testing.  It will return a reconciler that will use the Client for both local and remote calls.
func newTestReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileStatefulSet{
		Client: mgr.GetClient(),
		scheme: mgr.GetScheme(),
		log:    logging.New(),
		auth:   authorizer.NewFake(mgr.GetClient()),
	}
}

func TestReconcile(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	cluster := &clusterv1alpha1.EKS{
		ObjectMeta: metav1.ObjectMeta{Name: "foo-eks", Namespace: "default"},
		Spec: clusterv1alpha1.EKSSpec{
			AccountID:            "1234",
			ControlPlane:         clusterv1alpha1.ControlPlaneSpec{},
			CrossAccountRoleName: "foo-role",
			NodeGroups:           []clusterv1alpha1.NodeGroupSpec{},
			Region:               "us-test-1",
		},
	}

	instance := &componentsv1alpha1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "foo-statefulset", Namespace: "default"},
		Spec: componentsv1alpha1.StatefulSetSpec{
			Cluster:   "foo-eks",
			Name:      "remote-foo-statefulset",
			Namespace: "default",
			StatefulSetSpec: appsv1.StatefulSetSpec{
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app": "remote-foo-statefulset"},
				},
				ServiceName: "remote-foo-statefulset-service",
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "remote-foo-statefulset"}},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "nginx",
								Image: "nginx",
							},
						},
					},
				},
			},
		},
	}

	// Setup the Manager and Controller.  Wrap the Controller Reconcile function so it writes each request to a
	// channel when it is finished.
	mgr, err := manager.New(cfg, manager.Options{})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	c = mgr.GetClient()

	recFn, requests := SetupTestReconcile(newTestReconciler(mgr))
	g.Expect(add(mgr, recFn)).NotTo(gomega.HaveOccurred())

	stopMgr, mgrStopped := StartTestManager(mgr, g)

	defer func() {
		close(stopMgr)
		mgrStopped.Wait()
	}()

	g.Expect(c.Create(context.TODO(), cluster)).NotTo(gomega.HaveOccurred())
	defer c.Delete(context.TODO(), cluster)

	// Create the StatefulSet object and expect the Reconcile and Deployment to be created
	err = c.Create(context.TODO(), instance)
	// The instance object may not be a valid object because it might be missing some required fields.
	// Please modify the instance object by adding required fields and then remove the following if statement.
	if apierrors.IsInvalid(err) {
		t.Logf("failed to create object, got an invalid object error: %v", err)
		t.Fail()
		return
	}

	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Eventually(requests, timeout).Should(gomega.Receive(gomega.Equal(expectedRequest)))

	rStatefulSet := &appsv1.StatefulSet{}
	err = c.Get(context.TODO(), rSfsKey, rStatefulSet)
	g.Expect(err).NotTo(gomega.HaveOccurred())

	statefulSet := &componentsv1alpha1.StatefulSet{}
	g.Eventually(func() (string, error) {
		err := c.Get(context.TODO(), sfsKey, statefulSet)
		return statefulSet.Status.Status, err
	}, timeout).Should(gomega.Equal("Created"))

	g.Expect(c.Delete(context.TODO(), instance)).Should(gomega.Succeed())
	g.Eventually(requests, timeout).Should(gomega.Receive(gomega.Equal(expectedRequest)))
	g.Eventually(func() error { return c.Get(context.TODO(), rSfsKey, rStatefulSet) }, timeout).Should(gomega.HaveOccurred())
}
