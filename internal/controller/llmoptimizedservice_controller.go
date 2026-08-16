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
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
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
//+kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors,verbs=get;list;watch;create;update;patch;delete

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
		// Replicas is deliberately left untouched here: once the KEDA ScaledObject
		// targets this Deployment, .spec.replicas is owned by KEDA/HPA. Overwriting
		// it back to the CR's static MinReplicas on every reconcile would fight the
		// autoscaler and immediately undo any scale-up.
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
	} else if svc := r.serviceForM5(&aiService); !reflect.DeepEqual(foundService.Labels, svc.Labels) ||
		!reflect.DeepEqual(foundService.Spec.Selector, svc.Spec.Selector) ||
		!reflect.DeepEqual(foundService.Spec.Ports, svc.Spec.Ports) ||
		foundService.Spec.Type != svc.Spec.Type {
		logger.Info("Service drifted from desired spec, updating", "Name", foundService.Name)
		foundService.Labels = svc.Labels
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
		!reflect.DeepEqual(foundScaledObject.Spec.CooldownPeriod, so.Spec.CooldownPeriod) ||
		!reflect.DeepEqual(foundScaledObject.Spec.PollingInterval, so.Spec.PollingInterval) ||
		!reflect.DeepEqual(foundScaledObject.Spec.Triggers, so.Spec.Triggers) {
		logger.Info("ScaledObject drifted from desired spec, updating", "Name", foundScaledObject.Name)
		foundScaledObject.Spec.ScaleTargetRef = so.Spec.ScaleTargetRef
		foundScaledObject.Spec.MinReplicaCount = so.Spec.MinReplicaCount
		foundScaledObject.Spec.MaxReplicaCount = so.Spec.MaxReplicaCount
		foundScaledObject.Spec.CooldownPeriod = so.Spec.CooldownPeriod
		foundScaledObject.Spec.PollingInterval = so.Spec.PollingInterval
		foundScaledObject.Spec.Triggers = so.Spec.Triggers
		if err := r.Update(ctx, &foundScaledObject); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// 4. Reconcile ServiceMonitor (scrapes the queue-exporter sidecar for KEDA's Prometheus trigger)
	var foundServiceMonitor monitoringv1.ServiceMonitor
	err = r.Get(ctx, client.ObjectKey{Name: aiService.Name, Namespace: aiService.Namespace}, &foundServiceMonitor)
	if err != nil && errors.IsNotFound(err) {
		sm := r.serviceMonitorForM5(&aiService)
		logger.Info("Generating ServiceMonitor for queue-length metrics", "Name", sm.Name)
		if err := r.Create(ctx, sm); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	} else if sm := r.serviceMonitorForM5(&aiService); !reflect.DeepEqual(foundServiceMonitor.Spec.Selector, sm.Spec.Selector) ||
		!reflect.DeepEqual(foundServiceMonitor.Spec.Endpoints, sm.Spec.Endpoints) ||
		!reflect.DeepEqual(foundServiceMonitor.Spec.TargetLabels, sm.Spec.TargetLabels) {
		logger.Info("ServiceMonitor drifted from desired spec, updating", "Name", foundServiceMonitor.Name)
		foundServiceMonitor.Spec.Selector = sm.Spec.Selector
		foundServiceMonitor.Spec.Endpoints = sm.Spec.Endpoints
		foundServiceMonitor.Spec.TargetLabels = sm.Spec.TargetLabels
		if err := r.Update(ctx, &foundServiceMonitor); err != nil {
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
// Replicas is deliberately excluded: KEDA's ScaledObject owns .spec.replicas
// after creation, and comparing it here would fight the autoscaler.
func deploymentDrifted(found, desired *appsv1.Deployment) bool {
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

	minReplicaCount := m.Spec.MinReplicas
	var cooldownPeriod *int32
	if m.Spec.ScaleToZero {
		var zero int32 = 0
		minReplicaCount = &zero

		idleTimeout := int32(300)
		if m.Spec.IdleTimeoutSeconds != nil {
			idleTimeout = *m.Spec.IdleTimeoutSeconds
		}
		cooldownPeriod = &idleTimeout
	}

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
			MinReplicaCount: minReplicaCount,
			MaxReplicaCount: m.Spec.MaxReplicas,
			CooldownPeriod:  cooldownPeriod,
			// Custom AI metric trigger: Query Prometheus for current queue length
			Triggers: []kedav1alpha1.ScaleTriggers{{
				Type: "prometheus",
				Metadata: map[string]string{
					"serverAddress": "http://prometheus-k8s.monitoring.svc.cluster.local:9090",
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

// Scrapes the queue-exporter sidecar's /metrics endpoint and stamps the Service's
// "app" label onto each sample, so KEDA's PromQL query (app="<name>") can find it.
func (r *LLMOptimizedServiceReconciler) serviceMonitorForM5(m *aiv1alpha1.LLMOptimizedService) *monitoringv1.ServiceMonitor {
	sm := &monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name,
			Namespace: m.Namespace,
		},
		Spec: monitoringv1.ServiceMonitorSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{"app": m.Name},
			},
			Endpoints: []monitoringv1.Endpoint{{
				Port:     "metrics-port",
				Path:     "/metrics",
				Interval: "15s",
			}},
			TargetLabels: []string{"app"},
		},
	}

	_ = ctrl.SetControllerReference(m, sm, r.Scheme)
	return sm
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
					Containers: []corev1.Container{
						{
							Image: "ollama/ollama:latest",
							Name:  "ollama-engine",
							Ports: []corev1.ContainerPort{{ContainerPort: 11434, Name: "api-port"}},
							Env: []corev1.EnvVar{
								{Name: "OLLAMA_NUM_PARALLEL", Value: strconv.Itoa(int(m.Spec.MaxConcurrencyPerPod))},
								{Name: "OLLAMA_ORIGINS", Value: "*"},
							},
							VolumeMounts: []corev1.VolumeMount{{Name: "cache-mount", MountPath: "/root/.ollama"}},
						},
						{
							Image: "ollama-queue-exporter:local",
							Name:  "queue-exporter",
							Ports: []corev1.ContainerPort{
								{ContainerPort: 8081, Name: "proxy-port"},
								{ContainerPort: 9113, Name: "metrics-port"},
							},
						},
					},
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
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name,
			Namespace: m.Namespace,
			// Selected by the ServiceMonitor, and copied onto scraped metric
			// samples via its targetLabels so KEDA's PromQL query can filter on it.
			Labels: map[string]string{"app": m.Name},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": m.Name},
			Ports: []corev1.ServicePort{
				// Routed through the queue-exporter sidecar (not straight to Ollama)
				// so it can count in-flight requests for the metrics-port below.
				{Name: "api-port", Port: 11434, TargetPort: intstr.FromString("proxy-port"), Protocol: corev1.ProtocolTCP},
				{Name: "metrics-port", Port: 9113, TargetPort: intstr.FromString("metrics-port"), Protocol: corev1.ProtocolTCP},
			},
			Type: corev1.ServiceTypeClusterIP,
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
		Owns(&monitoringv1.ServiceMonitor{}).
		Complete(r)
}
