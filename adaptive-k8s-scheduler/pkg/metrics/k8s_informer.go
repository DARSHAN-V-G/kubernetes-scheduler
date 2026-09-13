package metrics

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	appslisters "k8s.io/client-go/listers/apps/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	policylisters "k8s.io/client-go/listers/policy/v1"
	"k8s.io/client-go/tools/cache"
	"go.uber.org/zap"
)

// K8sInformerManager manages client-go informers for Nodes, Pods, PDBs, and controllers.
type K8sInformerManager struct {
	client          kubernetes.Interface
	metricsCache    *MetricsCache
	logger          *zap.Logger
	factory         informers.SharedInformerFactory
	podLister       corelisters.PodLister
	nodeLister      corelisters.NodeLister
	pdbLister       policylisters.PodDisruptionBudgetLister
	rsLister        appslisters.ReplicaSetLister
	deployLister    appslisters.DeploymentLister
	stsLister       appslisters.StatefulSetLister
}

// NewK8sInformerManager constructs an informer manager with a default resync period.
func NewK8sInformerManager(client kubernetes.Interface, metricsCache *MetricsCache, resync time.Duration, logger *zap.Logger) *K8sInformerManager {
	if logger == nil {
		logger = zap.NewNop()
	}
	if resync <= 0 {
		resync = 30 * time.Second
	}

	factory := informers.NewSharedInformerFactory(client, resync)

	podInformer := factory.Core().V1().Pods()
	nodeInformer := factory.Core().V1().Nodes()
	pdbInformer := factory.Policy().V1().PodDisruptionBudgets()
	rsInformer := factory.Apps().V1().ReplicaSets()
	deployInformer := factory.Apps().V1().Deployments()
	stsInformer := factory.Apps().V1().StatefulSets()

	m := &K8sInformerManager{
		client:       client,
		metricsCache: metricsCache,
		logger:       logger,
		factory:      factory,
		podLister:    podInformer.Lister(),
		nodeLister:   nodeInformer.Lister(),
		pdbLister:    pdbInformer.Lister(),
		rsLister:     rsInformer.Lister(),
		deployLister: deployInformer.Lister(),
		stsLister:    stsInformer.Lister(),
	}

	// Register Pod Event Handlers
	podInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if pod, ok := obj.(*corev1.Pod); ok {
				m.syncPod(pod)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if pod, ok := newObj.(*corev1.Pod); ok {
				m.syncPod(pod)
			}
		},
		DeleteFunc: func(obj interface{}) {
			var pod *corev1.Pod
			switch t := obj.(type) {
			case *corev1.Pod:
				pod = t
			case cache.DeletedFinalStateUnknown:
				if p, ok := t.Obj.(*corev1.Pod); ok {
					pod = p
				}
			}
			if pod != nil {
				m.metricsCache.DeletePod(pod.Namespace, pod.Name)
				if pod.Spec.NodeName != "" {
					m.metricsCache.RecalculateNodeHeadroom(pod.Spec.NodeName)
				}
			}
		},
	})

	// Register Node Event Handlers
	nodeInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if node, ok := obj.(*corev1.Node); ok {
				m.syncNode(node)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if node, ok := newObj.(*corev1.Node); ok {
				m.syncNode(node)
			}
		},
		DeleteFunc: func(obj interface{}) {
			var node *corev1.Node
			switch t := obj.(type) {
			case *corev1.Node:
				node = t
			case cache.DeletedFinalStateUnknown:
				if n, ok := t.Obj.(*corev1.Node); ok {
					node = n
				}
			}
			if node != nil {
				m.metricsCache.DeleteNode(node.Name)
			}
		},
	})

	return m
}

// Start launches the informer factories and waits for caches to sync.
func (m *K8sInformerManager) Start(ctx context.Context) error {
	m.logger.Info("Starting K8s Informers...")
	m.factory.Start(ctx.Done())

	synced := m.factory.WaitForCacheSync(ctx.Done())
	for typ, ok := range synced {
		if !ok {
			return fmt.Errorf("informer failed to sync: %v", typ)
		}
	}
	m.logger.Info("All K8s Informers synced successfully")
	return nil
}

// syncNode extracts allocatable capacities, conditions, and stores NodeMetrics.
func (m *K8sInformerManager) syncNode(node *corev1.Node) {
	if node == nil {
		return
	}

	allocCPU := node.Status.Allocatable.Cpu().MilliValue()
	allocMem := node.Status.Allocatable.Memory().Value()
	capCPU := node.Status.Capacity.Cpu().MilliValue()
	capMem := node.Status.Capacity.Memory().Value()

	var isReady bool
	var hasMemPressure bool
	var hasDiskPressure bool
	var hasPIDPressure bool

	for _, cond := range node.Status.Conditions {
		switch cond.Type {
		case corev1.NodeReady:
			isReady = cond.Status == corev1.ConditionTrue
		case corev1.NodeMemoryPressure:
			hasMemPressure = cond.Status == corev1.ConditionTrue
		case corev1.NodeDiskPressure:
			hasDiskPressure = cond.Status == corev1.ConditionTrue
		case corev1.NodePIDPressure:
			hasPIDPressure = cond.Status == corev1.ConditionTrue
		}
	}

	existing, found := m.metricsCache.GetNode(node.Name)
	nodeMetrics := &NodeMetrics{
		Name:                     node.Name,
		AllocatableCPUMillis:     allocCPU,
		AllocatableMemoryBytes:   allocMem,
		TotalCapacityCPUMillis:   capCPU,
		TotalCapacityMemoryBytes: capMem,
		Conditions:               node.Status.Conditions,
		IsReady:                  isReady,
		HasMemoryPressure:        hasMemPressure,
		HasDiskPressure:          hasDiskPressure,
		HasPIDPressure:           hasPIDPressure,
		LastUpdated:              time.Now(),
	}

	if found {
		// Preserve physical telemetry
		nodeMetrics.ActualUsageCPUMillicores = existing.ActualUsageCPUMillicores
		nodeMetrics.ActualUsageMemoryBytes = existing.ActualUsageMemoryBytes
	}

	m.metricsCache.SetNode(nodeMetrics)
	m.metricsCache.RecalculateNodeHeadroom(node.Name)
}

// syncPod extracts control plane state (Priority, QoS, PDBs, Replicas) and container specs.
func (m *K8sInformerManager) syncPod(pod *corev1.Pod) {
	if pod == nil {
		return
	}

	var priorityVal int32
	if pod.Spec.Priority != nil {
		priorityVal = *pod.Spec.Priority
	}

	var totalReqCPU int64
	var totalLimitCPU int64
	var totalReqMem int64
	var totalLimitMem int64

	containerMap := make(map[string]*ContainerMetrics, len(pod.Spec.Containers))
	existing, hasExisting := m.metricsCache.GetPod(pod.Namespace, pod.Name)

	for _, c := range pod.Spec.Containers {
		reqCPU := c.Resources.Requests.Cpu().MilliValue()
		limitCPU := c.Resources.Limits.Cpu().MilliValue()
		reqMem := c.Resources.Requests.Memory().Value()
		limitMem := c.Resources.Limits.Memory().Value()

		totalReqCPU += reqCPU
		totalLimitCPU += limitCPU
		totalReqMem += reqMem
		totalLimitMem += limitMem

		cMetric := &ContainerMetrics{
			Name:                 c.Name,
			Image:                c.Image,
			RequestedCPUMillis:   reqCPU,
			LimitCPUMillis:       limitCPU,
			RequestedMemoryBytes: reqMem,
			LimitMemoryBytes:     limitMem,
			LastUpdated:          time.Now(),
		}

		if hasExisting && existing.Containers != nil {
			if prev, exists := existing.Containers[c.Name]; exists {
				cMetric.UsageCPUMillicores = prev.UsageCPUMillicores
				cMetric.UsageMemoryBytes = prev.UsageMemoryBytes
				cMetric.UsageRSSBytes = prev.UsageRSSBytes
				cMetric.TelemetryReady = prev.TelemetryReady
			}
		}

		containerMap[c.Name] = cMetric
	}

	// Resolve matching PDB
	disruptionsAllowed := m.resolveDisruptionsAllowed(pod)

	// Resolve replica quorum
	replicaInfo := m.resolveReplicaInfo(pod)

	podMetric := &PodMetrics{
		Namespace:               pod.Namespace,
		Name:                    pod.Name,
		UID:                     pod.UID,
		NodeName:                pod.Spec.NodeName,
		Phase:                   pod.Status.Phase,
		Labels:                  pod.Labels,
		Annotations:             pod.Annotations,
		QoSClass:                pod.Status.QOSClass,
		Priority:                priorityVal,
		PriorityClassName:       pod.Spec.PriorityClassName,
		DisruptionsAllowed:      disruptionsAllowed,
		Replicas:                replicaInfo,
		Containers:              containerMap,
		TotalRequestedCPUMillis: totalReqCPU,
		TotalLimitCPUMillis:     totalLimitCPU,
		TotalRequestedMemory:    totalReqMem,
		TotalLimitMemory:        totalLimitMem,
		LastUpdated:             time.Now(),
	}

	if hasExisting {
		// Preserve telemetry and idle calculation state
		podMetric.TotalUsageCPUMillicores = existing.TotalUsageCPUMillicores
		podMetric.TotalUsageMemoryBytes = existing.TotalUsageMemoryBytes
		podMetric.TotalUsageRSSBytes = existing.TotalUsageRSSBytes
		podMetric.NetworkRxBytesPerSec = existing.NetworkRxBytesPerSec
		podMetric.NetworkTxBytesPerSec = existing.NetworkTxBytesPerSec
		podMetric.TotalNetworkBytesSec = existing.TotalNetworkBytesSec
		podMetric.RequestQPS = existing.RequestQPS
		podMetric.IdleDuration = existing.IdleDuration
		podMetric.LastActiveTime = existing.LastActiveTime
		podMetric.IsIdle = existing.IsIdle
		podMetric.TelemetryReady = existing.TelemetryReady
	} else {
		podMetric.LastActiveTime = time.Now()
	}

	m.metricsCache.SetPod(podMetric)
	if pod.Spec.NodeName != "" {
		m.metricsCache.RecalculateNodeHeadroom(pod.Spec.NodeName)
	}
}

// resolveDisruptionsAllowed finds any matching PDB and returns disruptionsAllowed.
func (m *K8sInformerManager) resolveDisruptionsAllowed(pod *corev1.Pod) int32 {
	if m.pdbLister == nil || pod.Labels == nil {
		return -1
	}

	pdbs, err := m.pdbLister.PodDisruptionBudgets(pod.Namespace).List(labels.Everything())
	if err != nil {
		return -1
	}

	for _, pdb := range pdbs {
		if pdb.Spec.Selector == nil {
			continue
		}
		selector, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
		if err != nil {
			continue
		}
		if selector.Matches(labels.Set(pod.Labels)) {
			return pdb.Status.DisruptionsAllowed
		}
	}

	return -1 // No matching PDB
}

// resolveReplicaInfo traverses ownerReferences to find the owning Deployment, ReplicaSet, or StatefulSet.
func (m *K8sInformerManager) resolveReplicaInfo(pod *corev1.Pod) *ReplicaInfo {
	if pod == nil || len(pod.OwnerReferences) == 0 {
		return nil
	}

	for _, owner := range pod.OwnerReferences {
		switch owner.Kind {
		case "ReplicaSet":
			rs, err := m.rsLister.ReplicaSets(pod.Namespace).Get(owner.Name)
			if err == nil && rs != nil {
				var desired int32 = 1
				if rs.Spec.Replicas != nil {
					desired = *rs.Spec.Replicas
				}

				// Check if this ReplicaSet is owned by a Deployment
				for _, rsOwner := range rs.OwnerReferences {
					if rsOwner.Kind == "Deployment" {
						deploy, err := m.deployLister.Deployments(pod.Namespace).Get(rsOwner.Name)
						if err == nil && deploy != nil {
							var depDesired int32 = 1
							if deploy.Spec.Replicas != nil {
								depDesired = *deploy.Spec.Replicas
							}
							return &ReplicaInfo{
								OwnerKind:         "Deployment",
								OwnerName:         deploy.Name,
								DesiredReplicas:   depDesired,
								ReadyReplicas:     deploy.Status.ReadyReplicas,
								AvailableReplicas: deploy.Status.AvailableReplicas,
							}
						}
					}
				}

				return &ReplicaInfo{
					OwnerKind:         "ReplicaSet",
					OwnerName:         rs.Name,
					DesiredReplicas:   desired,
					ReadyReplicas:     rs.Status.ReadyReplicas,
					AvailableReplicas: rs.Status.AvailableReplicas,
				}
			}

		case "StatefulSet":
			sts, err := m.stsLister.StatefulSets(pod.Namespace).Get(owner.Name)
			if err == nil && sts != nil {
				var desired int32 = 1
				if sts.Spec.Replicas != nil {
					desired = *sts.Spec.Replicas
				}
				return &ReplicaInfo{
					OwnerKind:         "StatefulSet",
					OwnerName:         sts.Name,
					DesiredReplicas:   desired,
					ReadyReplicas:     sts.Status.ReadyReplicas,
					AvailableReplicas: sts.Status.AvailableReplicas,
				}
			}
		}
	}

	return nil
}
