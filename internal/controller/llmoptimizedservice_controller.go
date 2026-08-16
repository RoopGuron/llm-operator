package controller

import (
	"context"
	"reflect"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"strconv"

	kedav1alpha1 "github.com/kedacore/keda/v2/apis/keda/v1alpha1"
	aiv1alpha1 "mlo.platform/llm-operator/api/v1alpha1"
)

type LLMOptimizedServiceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=ai.mlo.platform,resources=llmoptimizedservices,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ai.mlo.platform,resources=llmoptimizedservices/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=keda.sh,resources=scaledobjects,verbs=get;list;watch;create;update;patch;delete

func (r *LLMOptimizedServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var aiService aiv1alpha1.LLMOptimizedService
	if err := r.Get(ctx, req.NamespacedName, &aiService); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("Syncing Platform Resource", "Model", aiService.Spec.ModelPath)

	// 1. Reconcile Deployment
	var foundDeployment appsv1.Deployment
	err := r.Get(ctx, client.ObjectKey{Name: aiService.Name, Namespace: aiService.Namespace}, &foundDeployment)
	if err != nil && errors.IsNotFound(err) {
		dep := r.deploymentForM5(&aiService)
		if err := r.Create(ctx, dep); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	} else if dep := r.deploymentForM5(&aiService); deploymentDrifted(&foundDeployment, dep) {
		logger.Info("Deployment drifted from desired spec, updating", "Name", foundDeployment.Name)
		foundDeployment.Spec.Replicas = dep.Spec.Replicas
		foundDeployment.Spec.Template.Spec.InitContainers = dep.Spec.Template.Spec.InitContainers
		foundDeployment.Spec.Template.Spec.Containers = dep.Spec.Template.Spec.Containers
		foundDeployment.Spec.Template.Spec.Volumes = dep.Spec.Template.Spec.Volumes
		if err := r.Update(ctx, &foundDeployment); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// 2. Reconcile ClusterIP Service
	var foundService corev1.Service
	err = r.Get(ctx, client.ObjectKey{Name: aiService.Name, Namespace: aiService.Namespace}, &foundService)
	if err != nil && errors.IsNotFound(err) {
		svc := r.serviceForM5(&aiService)
		if err := r.Create(ctx, svc); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	} else if svc := r.serviceForM5(&aiService); !reflect.DeepEqual(foundService.Spec.Selector, svc.Spec.Selector) ||
		!reflect.DeepEqual(foundService.Spec.Ports, svc.Spec.Ports) ||
		foundService.Spec.Type != svc.Spec.Type {
		logger.Info("Service drifted from desired spec, updating", "Name", foundService.Name)
		foundService.Spec.Selector = svc.Spec.Selector
		foundService.Spec.Ports = svc.Spec.Ports
		foundService.Spec.Type = svc.Spec.Type
		if err := r.Update(ctx, &foundService); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// 3. Reconcile KEDA ScaledObject
	var foundScaledObject kedav1alpha1.ScaledObject
	err = r.Get(ctx, client.ObjectKey{Name: aiService.Name, Namespace: aiService.Namespace}, &foundScaledObject)
	if err != nil && errors.IsNotFound(err) {
		so := r.scaledObjectForM5(&aiService)
		logger.Info("Generating KEDA Intelligent ScaledObject for LLM Workload", "Name", so.Name)
		if err := r.Create(ctx, so); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	} else if so := r.scaledObjectForM5(&aiService); !reflect.DeepEqual(foundScaledObject.Spec.ScaleTargetRef, so.Spec.ScaleTargetRef) ||
		!reflect.DeepEqual(foundScaledObject.Spec.MinReplicaCount, so.Spec.MinReplicaCount) ||
		!reflect.DeepEqual(foundScaledObject.Spec.MaxReplicaCount, so.Spec.MaxReplicaCount) ||
		!reflect.DeepEqual(foundScaledObject.Spec.Triggers, so.Spec.Triggers) {
		logger.Info("ScaledObject drifted from desired spec, updating", "Name", foundScaledObject.Name)
		foundScaledObject.Spec.ScaleTargetRef = so.Spec.ScaleTargetRef
		foundScaledObject.Spec.MinReplicaCount = so.Spec.MinReplicaCount
		foundScaledObject.Spec.MaxReplicaCount = so.Spec.MaxReplicaCount
		foundScaledObject.Spec.Triggers = so.Spec.Triggers
		if err := r.Update(ctx, &foundScaledObject); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

// deploymentDrifted reports whether the live Deployment's operator-managed fields
// differ from the desired state. It only compares fields this controller sets
// (rather than the whole PodSpec) so that fields the API server defaults on
// Create/Update don't register as permanent drift and cause an update loop.
func deploymentDrifted(found, desired *appsv1.Deployment) bool {
	if !reflect.DeepEqual(found.Spec.Replicas, desired.Spec.Replicas) {
		return true
	}
	if len(found.Spec.Template.Spec.InitContainers) != len(desired.Spec.Template.Spec.InitContainers) ||
		len(found.Spec.Template.Spec.Containers) != len(desired.Spec.Template.Spec.Containers) {
		return true
	}
	for i, c := range desired.Spec.Template.Spec.InitContainers {
		fc := found.Spec.Template.Spec.InitContainers[i]
		if fc.Image != c.Image || !reflect.DeepEqual(fc.Command, c.Command) || !reflect.DeepEqual(fc.RestartPolicy, c.RestartPolicy) {
			return true
		}
	}
	for i, c := range desired.Spec.Template.Spec.Containers {
		fc := found.Spec.Template.Spec.Containers[i]
		if fc.Image != c.Image || !reflect.DeepEqual(fc.Env, c.Env) || !reflect.DeepEqual(fc.VolumeMounts, c.VolumeMounts) {
			return true
		}
	}
	if !reflect.DeepEqual(found.Spec.Template.Spec.Volumes, desired.Spec.Template.Spec.Volumes) {
		return true
	}
	return false
}

// Generate the specific KEDA manifest tracking model concurrency
func (r *LLMOptimizedServiceReconciler) scaledObjectForM5(m *aiv1alpha1.LLMOptimizedService) *kedav1alpha1.ScaledObject {
	// Construct threshold targets from user manifest
	concurrencyThreshold := strconv.Itoa(int(m.Spec.MaxConcurrencyPerPod))

	so := &kedav1alpha1.ScaledObject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name,
			Namespace: m.Namespace,
		},
		Spec: kedav1alpha1.ScaledObjectSpec{
			ScaleTargetRef: &kedav1alpha1.ScaleTarget{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       m.Name, // Pointing directly to our inference pod deployment
			},
			MinReplicaCount: m.Spec.MinReplicas,
			MaxReplicaCount: m.Spec.MaxReplicas,
			// Custom AI metric trigger: Query Prometheus for current queue length
			Triggers: []kedav1alpha1.ScaleTriggers{{
				Type: "prometheus",
				Metadata: map[string]string{
					"serverAddress": "http://cluster.local",
					"metricName":    "ollama_request_queue_length",
					"query":         "sum(ollama_request_queue_length{app=\"" + m.Name + "\"})",
					"threshold":     concurrencyThreshold,
				},
			}},
		},
	}

	_ = ctrl.SetControllerReference(m, so, r.Scheme)
	return so
}

func (r *LLMOptimizedServiceReconciler) deploymentForM5(m *aiv1alpha1.LLMOptimizedService) *appsv1.Deployment {
	labels := map[string]string{"app": m.Name}
	var replicas int32 = 1
	if m.Spec.MinReplicas != nil {
		replicas = *m.Spec.MinReplicas
	}
	hostPathType := corev1.HostPathDirectoryOrCreate

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: m.Name, Namespace: m.Namespace},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{{
						Name:    "cache-warm-puller",
						Image:   "curlimages/curl:latest",
						Command: []string{"sh", "-c", "until curl -s http://127.0.0.1:11434 > /dev/null; do sleep 1; done; curl -X POST http://127.0.0.1:11434/api/pull -d '{\"name\": \"" + m.Spec.ModelPath + "\"}'; sleep infinity"},
						// CRITICAL: Tells K8s this runs alongside the main container instead of blocking it
						RestartPolicy: func() *corev1.ContainerRestartPolicy {
							p := corev1.ContainerRestartPolicyAlways
							return &p
						}(),
					}},
					Containers: []corev1.Container{{
						Image: "ollama/ollama:latest",
						Name:  "ollama-engine",
						Ports: []corev1.ContainerPort{{ContainerPort: 11434, Name: "api-port"}},
						Env: []corev1.EnvVar{
							{Name: "OLLAMA_NUM_PARALLEL", Value: strconv.Itoa(int(m.Spec.MaxConcurrencyPerPod))},
							{Name: "OLLAMA_ORIGINS", Value: "*"},
						},
						VolumeMounts: []corev1.VolumeMount{{Name: "cache-mount", MountPath: "/root/.ollama"}},
					}},
					Volumes: []corev1.Volume{{
						Name: "cache-mount",
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{Path: "/mnt/ollama_cache", Type: &hostPathType},
						},
					}},
				},
			},
		},
	}
	_ = ctrl.SetControllerReference(m, dep, r.Scheme)
	return dep
}

func (r *LLMOptimizedServiceReconciler) serviceForM5(m *aiv1alpha1.LLMOptimizedService) *corev1.Service {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: m.Name, Namespace: m.Namespace},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": m.Name},
			Ports:    []corev1.ServicePort{{Port: 11434, TargetPort: intstr.FromString("api-port")}},
			Type:     corev1.ServiceTypeClusterIP,
		},
	}
	_ = ctrl.SetControllerReference(m, svc, r.Scheme)
	return svc
}

func (r *LLMOptimizedServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1alpha1.LLMOptimizedService{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&kedav1alpha1.ScaledObject{}).
		Complete(r)
}
