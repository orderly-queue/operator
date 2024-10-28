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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("Queue Controller", func() {
	Context("When reconciling a resource", func() {
		ctx := context.Background()

		It("should create a base queue with no addons", func() {
			By("Creating the resource")
			queue := BaseQueue()
			err := k8sClient.Create(ctx, queue)
			Expect(err).To(BeNil())
			defer k8sClient.Delete(ctx, queue)

			By("Reconciling the created resource")
			controllerReconciler := &QueueReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      queue.Name,
					Namespace: queue.Namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
