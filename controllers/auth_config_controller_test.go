package controllers

import (
	"context"
	"os"
	"testing"

	"github.com/kuadrant/authorino/api/v1beta1"
	"github.com/kuadrant/authorino/pkg/cache"
	mock_cache "github.com/kuadrant/authorino/pkg/cache/mocks"
	mocks "github.com/kuadrant/authorino/pkg/common/mocks"

	"github.com/golang/mock/gomock"
	"gotest.tools/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	controllerruntime "sigs.k8s.io/controller-runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	authConfig = v1beta1.AuthConfig{
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
					Name: "keycloak",
					Oidc: &v1beta1.Identity_OidcConfig{
						Endpoint: "http://127.0.0.1:9001/auth/realms/demo",
					},
				},
			},
			Metadata: []*v1beta1.Metadata{
				{
					Name: "userinfo",
					UserInfo: &v1beta1.Metadata_UserInfo{
						IdentitySource: "keycloak",
					},
				},
				{
					Name: "resource-data",
					UMA: &v1beta1.Metadata_UMA{
						Endpoint: "http://127.0.0.1:9001/auth/realms/demo",
						Credentials: &v1.LocalObjectReference{
							Name: "secret",
						},
					},
				},
			},
			Authorization: []*v1beta1.Authorization{
				{
					Name: "main-policy",
					OPA: &v1beta1.Authorization_OPA{
						InlineRego: `
			method = object.get(input.context.request.http, "method", "")
			path = object.get(input.context.request.http, "path", "")

			allow {
              method == "GET"
              path = "/allow"
          }`,
					},
				},
				{
					Name: "some-extra-rules",
					JSON: &v1beta1.Authorization_JSONPatternMatching{
						Rules: []v1beta1.Authorization_JSONPatternMatching_Rule{
							{
								Selector: "context.identity.role",
								Operator: "eq",
								Value:    "admin",
							},
							{
								Selector: "attributes.source.address.Address.SocketAddress.address",
								Operator: "eq",
								Value:    "80.133.21.75",
							},
						},
					}},
			},
		},
		Status: v1beta1.AuthConfigStatus{
			Ready: false,
		},
	}

	secret = v1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret",
			Namespace: "authorino",
		},
		Data: map[string][]byte{
			"clientID":     []byte("clientID"),
			"clientSecret": []byte("clientSecret"),
		},
	}
)

func TestMain(m *testing.M) {
	authServer := mocks.NewHttpServerMock("127.0.0.1:9001", map[string]mocks.HttpServerMockResponses{
		"/auth/realms/demo/.well-known/openid-configuration": {Status: 200, Body: `{ "issuer": "http://127.0.0.1:9001/auth/realms/demo" }`},
		"/auth/realms/demo/.well-known/uma2-configuration":   {Status: 200, Body: `{ "issuer": "http://127.0.0.1:9001/auth/realms/demo" }`},
	})
	defer authServer.Close()
	os.Exit(m.Run())
}

func setupEnvironment(t *testing.T, c cache.Cache) AuthConfigReconciler {
	scheme := runtime.NewScheme()
	_ = v1beta1.AddToScheme(scheme)
	_ = v1.AddToScheme(scheme)
	// Create a fake client with an auth config and a secret.
	client := fake.NewFakeClientWithScheme(scheme, &authConfig, &secret)

	return AuthConfigReconciler{
		Client: client,
		Log:    ctrl.Log.WithName("reconcilerTest"),
		Scheme: nil,
		Cache:  c,
	}
}

func TestReconcilerOk(t *testing.T) {
	r := setupEnvironment(t, cache.NewCache())

	result, err := r.Reconcile(context.Background(), controllerruntime.Request{
		NamespacedName: types.NamespacedName{
			Namespace: authConfig.Namespace,
			Name:      authConfig.Name,
		},
	})

	if err != nil {
		t.Error(err)
	}

	// Result should be empty
	assert.DeepEqual(t, result, ctrl.Result{})
}

func TestReconcilerMissingSecret(t *testing.T) {
	r := setupEnvironment(t, cache.NewCache())

	_ = r.Client.Delete(context.TODO(), &secret)

	result, err := r.Reconcile(context.Background(), controllerruntime.Request{
		NamespacedName: types.NamespacedName{
			Namespace: authConfig.Namespace,
			Name:      authConfig.Name,
		},
	})

	// Error should be "secret" not found.
	assert.Check(t, errors.IsNotFound(err))
	// Result should be empty
	assert.DeepEqual(t, result, ctrl.Result{})
}

func TestReconcilerNotFound(t *testing.T) {
	r := setupEnvironment(t, cache.NewCache())

	// Let's try to reconcile a non existing object.
	result, err := r.Reconcile(context.Background(), controllerruntime.Request{
		NamespacedName: types.NamespacedName{
			Namespace: authConfig.Namespace,
			Name:      "nonExistant",
		},
	})

	if err != nil {
		t.Error(err)
	}

	// Result should be empty
	assert.DeepEqual(t, result, ctrl.Result{})
}

func TestTranslateAuthConfig(t *testing.T) {
	// TODO
}

func TestHostColllision(t *testing.T) {
	mockController := gomock.NewController(t)
	defer mockController.Finish()

	c := mock_cache.NewMockCache(mockController)
	r := setupEnvironment(t, c)

	c.EXPECT().FindId("echo-api").Return("other-namespace/other-auth-config-with-same-host", true)

	result, err := r.Reconcile(context.Background(), controllerruntime.Request{
		NamespacedName: types.NamespacedName{
			Namespace: authConfig.Namespace,
			Name:      authConfig.Name,
		},
	})

	assert.DeepEqual(t, result, ctrl.Result{})
	assert.NilError(t, err)
}
