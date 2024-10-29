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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/orderly-queue/orderly/pkg/config"
	"gopkg.in/yaml.v3"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/orderly-queue/operator/api/v1beta1"
)

const (
	fn = "orderly.io/finalizer"
)

// QueueReconciler reconciles a Queue object
type QueueReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Version string
}

//+kubebuilder:rbac:groups=orderly.io,resources=queues,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=orderly.io,resources=queues/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=orderly.io,resources=queues/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=secrets,verbs=list;get;create;delete;update;watch
//+kubebuilder:rbac:groups="",resources=pods,verbs=list;get;create;delete;update;watch
//+kubebuilder:rbac:groups="",resources=services,verbs=list;get;create;delete;update;watch
//+kubebuilder:rbac:groups="networking.k8s.io",resources=ingresses,verbs=list;get;create;delete;update;watch
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=list;get;create;delete;update;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.3/pkg/reconcile
func (r *QueueReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var queue v1beta1.Queue
	if err := r.Get(ctx, req.NamespacedName, &queue); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	hash, err := r.hash(queue.Spec)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("%w: failed to hash queue spec", err)
	}

	if !queue.DeletionTimestamp.IsZero() {
		logger.Info("deleting queue")
		return r.handleDeletion(ctx, queue)
	}

	logger.Info("reconciling queue", "revision", hash)

	if queue.Status.ConfigRevision != hash {
		logger.Info("reconciling config")
		return r.reconcileConfig(ctx, queue, hash)
	}

	if queue.Status.DeploymentRevision != hash {
		logger.Info("reocnciling deployment")
		return r.reconcileDeployment(ctx, queue, hash)
	}

	if queue.Status.IngressRevision != hash {
		logger.Info("reconciling ingress")
		return r.reconcileIngress(ctx, queue, hash)
	}

	return ctrl.Result{}, nil
}

func (r *QueueReconciler) reconcileIngress(ctx context.Context, queue v1beta1.Queue, rev string) (ctrl.Result, error) {
	if queue.Spec.Ingress.Enabled {
		// TODO: input validation
		svc := r.buildService(queue)
		if err := r.persistsService(ctx, svc); err != nil {
			return ctrl.Result{}, fmt.Errorf("%w: failed to apply service", err)
		}
		ingress := r.buildIngress(queue)
		if err := r.persistsIngress(ctx, ingress); err != nil {
			return ctrl.Result{}, fmt.Errorf("%w: failed to apply ingress", err)
		}
	} else {
		if err := r.deleteIngress(ctx, queue); err != nil && !kerrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("%w: failed to remove ingress", err)
		}
	}
	queue.Status.IngressRevision = rev
	r.replaceStatus(&queue, metav1.Condition{
		Type:               "Ingress",
		Status:             metav1.ConditionTrue,
		Reason:             "IngressUpdated",
		Message:            "Ingress updated",
		LastTransitionTime: metav1.Now(),
	})
	return ctrl.Result{}, r.Client.Status().Update(ctx, &queue)
}

func (r *QueueReconciler) replaceStatus(queue *v1beta1.Queue, cond metav1.Condition) {
	for i, c := range queue.Status.Conditions {
		if c.Type == cond.Type {
			queue.Status.Conditions[i] = cond
			return
		}
	}
}

func (r *QueueReconciler) handleDeletion(ctx context.Context, queue v1beta1.Queue) (ctrl.Result, error) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      queue.Name,
			Namespace: queue.Namespace,
		},
	}
	if err := r.Client.Delete(ctx, dep); err != nil && !kerrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("%w: failed to delete deployment", err)
	}

	config := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.configName(queue),
			Namespace: queue.Namespace,
		},
	}
	if err := r.Client.Delete(ctx, config); err != nil && !kerrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("%w: failed to delete config", err)
	}

	if err := r.deleteIngress(ctx, queue); err != nil && !kerrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("%w: failed to remove ingress", err)
	}

	ops := "["
	for i, f := range queue.GetFinalizers() {
		if f == fn {
			ops += fmt.Sprintf(`{"op": "remove", "path": "/metadata/finalizers/%d"}`, i)
		}
	}
	ops += "]"
	if len(ops) == 2 {
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, r.Client.Patch(ctx, &queue, client.RawPatch(types.JSONPatchType, []byte(ops)))
}

func (r *QueueReconciler) reconcileDeployment(ctx context.Context, queue v1beta1.Queue, rev string) (ctrl.Result, error) {
	deployment, err := r.buildDeployment(queue)
	if err != nil {
		return ctrl.Result{}, err
	}

	exists := true
	err = r.Client.Get(ctx, types.NamespacedName{
		Name:      deployment.Name,
		Namespace: deployment.Namespace,
	}, &appsv1.Deployment{})
	if err != nil {
		if !kerrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("%w: failed to get existing deployment", err)
		}
		exists = false
	}

	if exists {
		if err := r.Client.Update(ctx, deployment); err != nil {
			return ctrl.Result{}, fmt.Errorf("%w: failed to update deployment", err)
		}
	} else {
		if err := r.Client.Create(ctx, deployment); err != nil {
			return ctrl.Result{}, fmt.Errorf("%w: failed to create new deployment", err)
		}
	}

	queue.Status.DeploymentRevision = rev
	r.replaceStatus(&queue, metav1.Condition{
		Type:               "Deployment",
		Status:             metav1.ConditionTrue,
		Reason:             "DeploymentUpdated",
		Message:            "Deployment updated",
		LastTransitionTime: metav1.Now(),
	})
	return ctrl.Result{}, r.Client.Status().Update(ctx, &queue)
}

func (r *QueueReconciler) reconcileConfig(ctx context.Context, queue v1beta1.Queue, rev string) (ctrl.Result, error) {
	encKey, err := r.getSecretValue(ctx, queue.Namespace, queue.Spec.EncryptionKey.SecretName, queue.Spec.EncryptionKey.SecretKey)
	if err != nil {
		return ctrl.Result{}, err
	}
	jwtSecret, err := r.getSecretValue(ctx, queue.Namespace, queue.Spec.JwtSecret.SecretName, queue.Spec.JwtSecret.SecretKey)
	if err != nil {
		return ctrl.Result{}, err
	}

	conf := r.defaultConfig()
	conf.EncryptionKey = encKey
	conf.JwtSecret = jwtSecret
	if err := r.buildConfig(queue, conf); err != nil {
		return ctrl.Result{}, err
	}
	conf.SetDefaults()

	if err := r.persistConfig(ctx, queue, conf); err != nil {
		return ctrl.Result{}, fmt.Errorf("%w: failed to persist config", err)
	}

	if err := r.restartDeployment(ctx, queue); err != nil {
		return ctrl.Result{}, fmt.Errorf("%w: failed to restart deployment", err)
	}

	queue.Status.ConfigRevision = rev
	r.replaceStatus(&queue, metav1.Condition{
		Type:               "Config",
		Status:             metav1.ConditionTrue,
		Reason:             "ConfigUpdated",
		Message:            "Config updated, queue restarted",
		LastTransitionTime: metav1.Now(),
	})

	return ctrl.Result{}, r.Client.Status().Update(ctx, &queue)
}

func (r *QueueReconciler) deleteIngress(ctx context.Context, queue v1beta1.Queue) error {
	svc := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      queue.Name,
			Namespace: queue.Namespace,
		},
	}
	if err := r.Client.Delete(ctx, svc); err != nil && !kerrors.IsNotFound(err) {
		return fmt.Errorf("%w: failed to delete service", err)
	}

	ing := &netv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      queue.Name,
			Namespace: queue.Namespace,
		},
	}
	if err := r.Client.Delete(ctx, ing); err != nil && !kerrors.IsNotFound(err) {
		return fmt.Errorf("%w: failed to delete ingress", err)
	}
	return nil
}

func (r *QueueReconciler) persistsIngress(ctx context.Context, ing *netv1.Ingress) error {
	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      ing.Name,
		Namespace: ing.Namespace,
	}, &netv1.Ingress{})
	if err != nil {
		if !kerrors.IsNotFound(err) {
			return fmt.Errorf("%w: could not get ingress", err)
		}
		return r.Client.Create(ctx, ing)
	}
	return r.Client.Update(ctx, ing)
}

func (r *QueueReconciler) buildIngress(queue v1beta1.Queue) *netv1.Ingress {
	class := "nginx"
	if queue.Spec.Ingress.IngressClass != "" {
		class = queue.Spec.Ingress.IngressClass
	}
	pt := netv1.PathTypeImplementationSpecific

	paths := []netv1.HTTPIngressPath{
		{
			Path:     "/",
			PathType: &pt,
			Backend: netv1.IngressBackend{
				Service: &netv1.IngressServiceBackend{
					Name: queue.Name,
					Port: netv1.ServiceBackendPort{
						Name: "http",
					},
				},
			},
		},
	}
	if queue.Spec.Ingress.ExposeMetrics {
		paths = append(paths, netv1.HTTPIngressPath{
			Path:     "/metrics",
			PathType: &pt,
			Backend: netv1.IngressBackend{
				Service: &netv1.IngressServiceBackend{
					Name: queue.Name,
					Port: netv1.ServiceBackendPort{
						Name: "metrics",
					},
				},
			},
		})
	}
	ing := &netv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      queue.Name,
			Namespace: queue.Namespace,
			Annotations: map[string]string{
				"kubernetes.io/tls-acme": "true",
			},
		},
		Spec: netv1.IngressSpec{
			IngressClassName: &class,
			TLS: []netv1.IngressTLS{
				{
					SecretName: fmt.Sprintf("%s-tls", strings.ReplaceAll(queue.Spec.Ingress.Host, ".", "-")),
					Hosts:      []string{queue.Spec.Ingress.Host},
				},
			},
			Rules: []netv1.IngressRule{
				{
					Host: queue.Spec.Ingress.Host,
					IngressRuleValue: netv1.IngressRuleValue{
						HTTP: &netv1.HTTPIngressRuleValue{
							Paths: paths,
						},
					},
				},
			},
		},
	}
	controllerutil.SetOwnerReference(&queue, ing, r.Scheme)
	return ing
}

func (r *QueueReconciler) persistsService(ctx context.Context, svc *v1.Service) error {
	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      svc.Name,
		Namespace: svc.Namespace,
	}, &v1.Service{})
	if err != nil {
		if !kerrors.IsNotFound(err) {
			return fmt.Errorf("%w: failed to get service", err)
		}
		return r.Client.Create(ctx, svc)
	}
	return r.Client.Update(ctx, svc)
}

func (r *QueueReconciler) buildService(queue v1beta1.Queue) *v1.Service {
	svc := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      queue.Name,
			Namespace: queue.Namespace,
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{
					Name:       "http",
					Port:       8765,
					TargetPort: intstr.FromString("http"),
				},
				{
					Name:       "metrics",
					Port:       8766,
					TargetPort: intstr.FromString("metrics"),
				},
			},
			Selector: r.labels(queue),
			Type:     v1.ServiceTypeClusterIP,
		},
	}
	controllerutil.SetOwnerReference(&queue, svc, r.Scheme)
	return svc
}

func (r *QueueReconciler) hash(obj any) (string, error) {
	by, err := json.Marshal(obj)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%s", r.Version, hash(by)), nil
}

func (r *QueueReconciler) labels(queue v1beta1.Queue) map[string]string {
	return map[string]string{
		"orderly.io/instance": queue.Name,
	}
}

func (r *QueueReconciler) buildDeployment(queue v1beta1.Queue) (*appsv1.Deployment, error) {
	repo := queue.Spec.Image.Repository
	if repo == "" {
		repo = "ghcr.io/orderly-queue/orderly"
	}
	tag := queue.Spec.Image.Tag
	pullPolicy := v1.PullIfNotPresent
	if tag == "" {
		tag = "latest"
		pullPolicy = v1.PullAlways
	}

	cpuLimt, err := resource.ParseQuantity(fmt.Sprintf("%d", queue.Spec.Resources.Limits.CPU))
	if err != nil {
		return nil, fmt.Errorf("%w: failed to parse cpu limits", err)
	}
	memLimit, err := resource.ParseQuantity(queue.Spec.Resources.Limits.Memory)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to parse memory limits", err)
	}

	resources := &v1.ResourceRequirements{
		Limits: v1.ResourceList{
			v1.ResourceCPU:    cpuLimt,
			v1.ResourceMemory: memLimit,
		},
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      queue.Name,
			Namespace: queue.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: replicas(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: r.labels(queue),
			},
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RecreateDeploymentStrategyType,
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: r.labels(queue),
					OwnerReferences: []metav1.OwnerReference{
						*metav1.NewControllerRef(&queue.ObjectMeta, schema.GroupVersionKind{
							Group:   v1beta1.GroupVersion.Group,
							Version: v1beta1.GroupVersion.Version,
							Kind:    "Queue",
						}),
					},
				},
				Spec: v1.PodSpec{
					Volumes: []v1.Volume{
						{
							Name: "config",
							VolumeSource: v1.VolumeSource{
								Secret: &v1.SecretVolumeSource{
									SecretName: r.configName(queue),
								},
							},
						},
					},
					Containers: []v1.Container{
						{
							Name:            "orderly",
							Image:           fmt.Sprintf("%s:%s", repo, tag),
							ImagePullPolicy: v1.PullPolicy(pullPolicy),
							Args:            []string{"serve", "--config", "/config/config.yaml"},
							Ports: []v1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: 8765,
								},
								{
									Name:          "metrics",
									ContainerPort: 8766,
								},
								{
									Name:          "probes",
									ContainerPort: 8767,
								},
							},
							LivenessProbe: &v1.Probe{
								ProbeHandler: v1.ProbeHandler{
									HTTPGet: &v1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromString("probes"),
									},
								},
							},
							ReadinessProbe: &v1.Probe{
								FailureThreshold: 10,
								ProbeHandler: v1.ProbeHandler{
									HTTPGet: &v1.HTTPGetAction{
										Path: "/readyz",
										Port: intstr.FromString("probes"),
									},
								},
							},
							VolumeMounts: []v1.VolumeMount{
								{
									Name:      "config",
									ReadOnly:  true,
									MountPath: "/config",
								},
							},
							Resources: *resources,
							Env: []v1.EnvVar{
								{
									Name: "GOMEMLIMIT",
									ValueFrom: &v1.EnvVarSource{
										ResourceFieldRef: &v1.ResourceFieldSelector{
											Resource: "limits.memory",
										},
									},
								},
								{
									Name: "GOMAXPROCS",
									ValueFrom: &v1.EnvVarSource{
										ResourceFieldRef: &v1.ResourceFieldSelector{
											Resource: "limits.cpu",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	controllerutil.SetOwnerReference(&queue, dep, r.Scheme)
	return dep, nil
}

func replicas(i int) *int32 {
	ii := int32(i)
	return &ii
}

func (r *QueueReconciler) persistConfig(ctx context.Context, queue v1beta1.Queue, conf *config.Config) error {
	name := r.configName(queue)
	marsh, err := yaml.Marshal(conf)
	if err != nil {
		return err
	}

	sec := &v1.Secret{}
	err = r.Client.Get(
		ctx,
		types.NamespacedName{
			Namespace: queue.Namespace,
			Name:      name,
		},
		sec,
	)
	create := false
	if err != nil {
		if !kerrors.IsNotFound(err) {
			return err
		}
		create = true
	}
	sec.Name = name
	sec.Namespace = queue.Namespace
	sec.Data = map[string][]byte{
		"config.yaml": marsh,
	}
	controllerutil.SetOwnerReference(&queue, sec, r.Scheme)

	if create {
		if err := r.Client.Create(ctx, sec); err != nil {
			return fmt.Errorf("%w: failed to create config secret", err)
		}
	}
	if err := r.Client.Update(ctx, sec); err != nil {
		return fmt.Errorf("%w: failed to update config secret", err)
	}
	return nil
}

func (r *QueueReconciler) configName(queue v1beta1.Queue) string {
	return fmt.Sprintf("%s-config", queue.Name)
}

func (r *QueueReconciler) buildConfig(queue v1beta1.Queue, conf *config.Config) error {
	conf.Name = queue.Name
	conf.Environment = "production"
	conf.Telemetry.Metrics.Enabled = true
	conf.LogLevel = "error"

	if queue.Spec.Storage.Enabled {
		stc := map[string]any{}
		for key, val := range queue.Spec.Storage.Config {
			if key == "insecrue" {
				switch val {
				case "true":
					stc[key] = true
				case "false":
					stc[key] = false
				default:
					return errors.New("invalid storage config value insecure")
				}
				continue
			}
			stc[key] = val
		}
		conf.Storage = config.Storage{
			Enabled: true,
			Type:    queue.Spec.Storage.Type,
			Config:  stc,
		}
	}
	return nil
}

func (r *QueueReconciler) defaultConfig() *config.Config {
	config := &config.Config{}
	config.SetDefaults()
	return config
}

func (r *QueueReconciler) getSecretValue(ctx context.Context, namespace string, name string, key string) (string, error) {
	s := &v1.Secret{}
	err := r.Client.Get(
		ctx,
		types.NamespacedName{
			Namespace: namespace,
			Name:      name,
		},
		s,
	)
	if err != nil {
		return "", err
	}

	pw, ok := s.Data[key]
	if !ok {
		return "", errors.New("could not find specified secret key")
	}

	return string(pw), nil
}

func (r *QueueReconciler) restartDeployment(ctx context.Context, queue v1beta1.Queue) error {
	patch := &appsv1.Deployment{
		Spec: appsv1.DeploymentSpec{
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"kubectl.kubernetes.io/restartedAt": time.Now().Format(time.RFC3339),
					},
				},
			},
		},
	}
	existing := &appsv1.Deployment{}
	if err := r.Client.Get(ctx, types.NamespacedName{
		Name:      queue.Name,
		Namespace: queue.Namespace,
	}, existing); err != nil {
		if kerrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("%w: failed to get existing deployment", err)
	}
	return r.Client.Patch(ctx, existing, client.MergeFrom(patch))
}

// SetupWithManager sets up the controller with the Manager.
func (r *QueueReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.Queue{}).
		Complete(r)
}

func hash(input []byte) string {
	sh := sha256.New()
	sh.Write(input)
	return hex.EncodeToString(sh.Sum(nil))
}
