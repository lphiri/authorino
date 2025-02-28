package controllers

import (
	"context"
	"testing"

	"gotest.tools/assert"

	"github.com/kuadrant/authorino/api/v1beta1"
	controller_builder "github.com/kuadrant/authorino/controllers/builder"
	mock_controller_builder "github.com/kuadrant/authorino/controllers/builder/mocks"

	"github.com/golang/mock/gomock"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	controllerruntime "sigs.k8s.io/controller-runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type fakeReconciler struct {
	Reconciled bool
	Finished   chan bool
}

func (r *fakeReconciler) Reconcile(_ context.Context, req ctrl.Request) (ctrl.Result, error) {
	defer close(r.Finished)
	r.Finished <- true
	r.Reconciled = true
	return ctrl.Result{}, nil
}

type isPredicate struct {
}

func (c *isPredicate) Matches(x interface{}) bool {
	predicateFuncs := x.(predicate.Funcs)
	return predicateFuncs.CreateFunc != nil // Not ideal, but since we're only adding one filter
}

func (c *isPredicate) String() string {
	return "contains 1 predicate"
}

type secretReconcilerTest struct {
	secret               v1.Secret
	authConfig           v1beta1.AuthConfig
	authConfigReconciler *fakeReconciler
	secretReconciler     *SecretReconciler
}

func newSecretReconcilerTest(secretLabels map[string]string) secretReconcilerTest {
	secret := v1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bill",
			Namespace: "authorino",
			Labels:    secretLabels,
		},
		Data: map[string][]byte{
			"api_key": []byte("123456"),
		},
	}

	authConfig := v1beta1.AuthConfig{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AuthConfig",
			APIVersion: "authorino.3scale.net/v1beta1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "auth-config-1",
			Namespace: "authorino",
		},
		Spec: v1beta1.AuthConfigSpec{
			Hosts: []string{"echo-api"},
			Identity: []*v1beta1.Identity{
				{
					Name: "friends",
					APIKey: &v1beta1.Identity_APIKey{
						LabelSelectors: map[string]string{
							"authorino.3scale.net/managed-by": "authorino",
							"target":                          "echo-api",
						},
					},
				},
			},
			Metadata:      []*v1beta1.Metadata{},
			Authorization: []*v1beta1.Authorization{},
		},
		Status: v1beta1.AuthConfigStatus{
			Ready: false,
		},
	}

	scheme := runtime.NewScheme()
	_ = v1beta1.AddToScheme(scheme)
	_ = v1.AddToScheme(scheme)
	// Create a fake client with an auth config and a secret.
	client := fake.NewFakeClientWithScheme(scheme, &authConfig, &secret)

	authConfigReconciler := &fakeReconciler{
		Finished: make(chan bool),
	}
	secretReconciler := &SecretReconciler{
		Client:               client,
		Log:                  ctrl.Log.WithName("reconcilerTest"),
		Scheme:               nil,
		SecretLabel:          "authorino.3scale.net/managed-by",
		AuthConfigReconciler: authConfigReconciler,
	}

	t := secretReconcilerTest{
		secret,
		authConfig,
		authConfigReconciler,
		secretReconciler,
	}

	return t
}

func TestSetupSecretReconcilerWithManager(t *testing.T) {
	reconcilerTest := newSecretReconcilerTest(map[string]string{
		"authorino.3scale.net/managed-by": "authorino",
	})
	secretReconciler := reconcilerTest.secretReconciler

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	builder := mock_controller_builder.NewMockControllerBuilder(mockCtrl)

	newController = func(m manager.Manager) controller_builder.ControllerBuilder {
		return builder
	}

	builder.EXPECT().For(gomock.Any()).Return(builder)
	builder.EXPECT().WithEventFilter(&isPredicate{}).Return(builder)
	builder.EXPECT().Complete(secretReconciler)

	_ = secretReconciler.SetupWithManager(nil)
}

func (t *secretReconcilerTest) reconcile() (reconcile.Result, error) {
	return t.secretReconciler.Reconcile(context.Background(), controllerruntime.Request{
		NamespacedName: types.NamespacedName{
			Namespace: t.secret.Namespace,
			Name:      t.secret.Name,
		},
	})
}

func TestMissingAuthorinoLabel(t *testing.T) {
	// secret missing the authorino "managed-by" label
	reconcilerTest := newSecretReconcilerTest(map[string]string{
		"authorino.3scale.net/managed-by": "authorino",
	})

	_, err := reconcilerTest.reconcile()

	assert.Check(t, !reconcilerTest.authConfigReconciler.Reconciled)
	assert.NilError(t, err)
}

func TestSameLabelsAsAuthConfig(t *testing.T) {
	// secret with the authorino "managed-by" label and the same labels as specified in the auth config
	reconcilerTest := newSecretReconcilerTest(map[string]string{
		"authorino.3scale.net/managed-by": "authorino",
		"target":                          "echo-api",
	})

	_, err := reconcilerTest.reconcile()

	finished := <-reconcilerTest.authConfigReconciler.Finished

	assert.Check(t, finished)
	assert.Check(t, reconcilerTest.authConfigReconciler.Reconciled)
	assert.NilError(t, err)
}

func TestUnmatchingLabels(t *testing.T) {
	// secret with the authorino "managed-by" label but not the same labels as specified in the auth config
	reconcilerTest := newSecretReconcilerTest(map[string]string{
		"authorino.3scale.net/managed-by": "authorino",
	})

	_, err := reconcilerTest.reconcile()

	assert.Check(t, !reconcilerTest.authConfigReconciler.Reconciled)
	assert.NilError(t, err)
}
