package dependency

import (
	"fmt"
	"sort"
)

// DependencyGraph manages nodes and edges between workloads.
type DependencyGraph struct {
	nodes map[WorkloadKey]*DependencyNode
}

// NewDependencyGraph creates an empty dependency graph.
func NewDependencyGraph() *DependencyGraph {
	return &DependencyGraph{
		nodes: make(map[WorkloadKey]*DependencyNode),
	}
}

// BuildGraph constructs a DependencyGraph from a list of workload descriptions.
func BuildGraph(workloads []WorkloadInfo) *DependencyGraph {
	g := NewDependencyGraph()
	for _, w := range workloads {
		if existing, exists := g.nodes[w.Key]; exists {
			if w.IsReady {
				existing.Info.IsReady = true
				existing.Info.IsHibernate = false
			}
			continue
		}
		g.nodes[w.Key] = &DependencyNode{
			Info:     w,
			Parents:  make(map[WorkloadKey]*DependencyNode),
			Children: make(map[WorkloadKey]*DependencyNode),
		}
	}

	// Link edges
	for _, node := range g.nodes {
		for _, depKey := range node.Info.Dependencies {
			depNode, exists := g.nodes[depKey]
			if !exists {
				// Create implicit node if not discovered yet
				depNode = &DependencyNode{
					Info: WorkloadInfo{
						Key:         depKey,
						ServiceName: depKey.Name,
						IsReady:     false,
						IsHibernate: true,
					},
					Parents:  make(map[WorkloadKey]*DependencyNode),
					Children: make(map[WorkloadKey]*DependencyNode),
				}
				g.nodes[depKey] = depNode
			}
			node.Children[depKey] = depNode
			depNode.Parents[node.Info.Key] = node
		}
	}

	return g
}

// GetNode returns a node by its key.
func (g *DependencyGraph) GetNode(key WorkloadKey) *DependencyNode {
	return g.nodes[key]
}

// ResolveRestorationStages calculates the ordered restoration stages for a target workload.
// Only unready/hibernated workloads in the dependency subtree are included.
func (g *DependencyGraph) ResolveRestorationStages(target WorkloadKey) (*RestorationPlan, error) {
	targetNode, exists := g.nodes[target]
	if !exists {
		return nil, fmt.Errorf("target workload %s not found in dependency graph", target)
	}

	// If target is already ready and running, no restoration is needed
	if targetNode.Info.IsReady && !targetNode.Info.IsHibernate {
		return &RestorationPlan{
			Target: target,
			Stages: nil,
		}, nil
	}

	// 1. Collect all transitive dependencies of target
	visited := make(map[WorkloadKey]bool)
	unreadySubGraph := make(map[WorkloadKey]*DependencyNode)

	var collectUnready func(node *DependencyNode)
	collectUnready = func(node *DependencyNode) {
		if visited[node.Info.Key] {
			return
		}
		visited[node.Info.Key] = true

		// If node is not ready or is hibernated, include in restoration graph
		if !node.Info.IsReady || node.Info.IsHibernate {
			unreadySubGraph[node.Info.Key] = node
		}

		for _, child := range node.Children {
			collectUnready(child)
		}
	}

	collectUnready(targetNode)

	// Ensure target itself is in the unready subgraph
	unreadySubGraph[target] = targetNode

	// 2. Compute in-degrees within the unready subgraph
	// Here an edge: Parent -> Child means Parent depends on Child.
	// So Child must be restored BEFORE Parent.
	// In topological terms for restoration order:
	// A node with 0 unready children is a leaf (ready to be restored immediately).
	unreadyChildrenCount := make(map[WorkloadKey]int)
	dependentParents := make(map[WorkloadKey][]WorkloadKey)

	for key, node := range unreadySubGraph {
		unreadyChildrenCount[key] = 0
		for childKey := range node.Children {
			if _, inSubGraph := unreadySubGraph[childKey]; inSubGraph {
				unreadyChildrenCount[key]++
				dependentParents[childKey] = append(dependentParents[childKey], key)
			}
		}
	}

	// 3. Layered Topological Sort (Kahn's Algorithm variant for parallel stages)
	var stages [][]WorkloadKey
	remainingCount := len(unreadySubGraph)

	for remainingCount > 0 {
		var currentStage []WorkloadKey
		for key, count := range unreadyChildrenCount {
			if count == 0 {
				currentStage = append(currentStage, key)
			}
		}

		if len(currentStage) == 0 {
			// Cycle detected among remaining unready nodes!
			// Group all remaining unready nodes into a single parallel stage to avoid deadlock.
			var cycleStage []WorkloadKey
			for key := range unreadyChildrenCount {
				cycleStage = append(cycleStage, key)
			}
			sort.Slice(cycleStage, func(i, j int) bool {
				return cycleStage[i].String() < cycleStage[j].String()
			})
			stages = append(stages, cycleStage)
			break
		}

		// Sort keys within stage for deterministic execution
		sort.Slice(currentStage, func(i, j int) bool {
			return currentStage[i].String() < currentStage[j].String()
		})

		stages = append(stages, currentStage)
		remainingCount -= len(currentStage)

		// Remove processed nodes and update parent dependencies
		for _, key := range currentStage {
			delete(unreadyChildrenCount, key)
			for _, parentKey := range dependentParents[key] {
				if _, ok := unreadyChildrenCount[parentKey]; ok {
					unreadyChildrenCount[parentKey]--
				}
			}
		}
	}

	return &RestorationPlan{
		Target: target,
		Stages: stages,
	}, nil
}
