/*
Copyright 2024.

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

package controller

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/brianvoe/gofakeit/v7"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/orderly-queue/operator/api/v1beta1"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var cfg *rest.Config
var k8sClient client.Client
var testEnv *envtest.Environment
var _ = gofakeit.Seed(0)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,

		// The BinaryAssetsDirectory is only required if you want to run the tests directly
		// without call the makefile target test. If not informed it will look for the
		// default path defined in controller-runtime which is /usr/local/kubebuilder/.
		// Note that you must have the required binaries setup under the bin directory to perform
		// the tests directly. When we run make test it will be setup and used automatically.
		BinaryAssetsDirectory: filepath.Join("..", "..", "bin", "k8s",
			fmt.Sprintf("1.29.0-%s-%s", runtime.GOOS, runtime.GOARCH)),
	}

	var err error
	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = v1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	base := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      "test-secrets",
			Namespace: "default",
		},
		StringData: map[string]string{
			"encryptionKey": "base64:32:eoN9P1NndyYjKoeIyoaKxmaVzYCz32ZEc9V0XmXlFM4=",
			"jwtSecret":     "base64:Hznayfuih4eLnZjtNGiwauq0y999FhJWKA8zGwymaoQ",
		},
	}
	err = k8sClient.Create(context.Background(), base)
	Expect(err).To(BeNil())

})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

func BaseQueue() *v1beta1.Queue {
	return &v1beta1.Queue{
		ObjectMeta: v1.ObjectMeta{
			Name:      strings.ToLower(fmt.Sprintf("%s-%s-%s", gofakeit.Word(), gofakeit.Word(), gofakeit.Word())),
			Namespace: "default",
		},
		Spec: v1beta1.QueueSpec{
			LogLevel: "debug",
			Snapshots: v1beta1.SnapshotSpec{
				Enabled: false,
			},
			Resources: v1beta1.ResourcesSpec{
				Limits: v1beta1.LimitsSpec{
					CPU:    1,
					Memory: "24Mi",
				},
			},
			EncryptionKey: v1beta1.SecretRef{
				SecretName: "test-secrets",
				SecretKey:  "encryptionKey",
			},
			JwtSecret: v1beta1.SecretRef{
				SecretName: "test-secrets",
				SecretKey:  "jwtSecret",
			},
			Storage: v1beta1.StorageSpec{
				Enabled: false,
			},
		},
	}
}
