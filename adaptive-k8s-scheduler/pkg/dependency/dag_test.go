package dependency

import (
	"testing"
)

func TestLinearDependency(t *testing.T) {
	// frontend -> backend -> postgres
	workloads := []WorkloadInfo{
		{
			Key:          WorkloadKey{Namespace: "ecommerce", Name: "frontend"},
			Dependencies: []WorkloadKey{{Namespace: "ecommerce", Name: "backend"}},
			IsReady:      false,
			IsHibernate:  true,
		},
		{
			Key:          WorkloadKey{Namespace: "ecommerce", Name: "backend"},
			Dependencies: []WorkloadKey{{Namespace: "ecommerce", Name: "postgres"}},
			IsReady:      false,
			IsHibernate:  true,
		},
		{
			Key:          WorkloadKey{Namespace: "ecommerce", Name: "postgres"},
			Dependencies: nil,
			IsReady:      false,
			IsHibernate:  true,
		},
	}

	g := BuildGraph(workloads)
	plan, err := g.ResolveRestorationStages(WorkloadKey{Namespace: "ecommerce", Name: "frontend"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plan.Stages) != 3 {
		t.Fatalf("expected 3 stages, got %d", len(plan.Stages))
	}

	// Stage 0: postgres
	if len(plan.Stages[0]) != 1 || plan.Stages[0][0].Name != "postgres" {
		t.Errorf("expected stage 0 to be postgres, got %v", plan.Stages[0])
	}
	// Stage 1: backend
	if len(plan.Stages[1]) != 1 || plan.Stages[1][0].Name != "backend" {
		t.Errorf("expected stage 1 to be backend, got %v", plan.Stages[1])
	}
	// Stage 2: frontend
	if len(plan.Stages[2]) != 1 || plan.Stages[2][0].Name != "frontend" {
		t.Errorf("expected stage 2 to be frontend, got %v", plan.Stages[2])
	}
}

func TestDiamondDependency(t *testing.T) {
	// App -> [Cache, Auth] -> DB
	workloads := []WorkloadInfo{
		{
			Key:          WorkloadKey{Namespace: "ecommerce", Name: "app"},
			Dependencies: []WorkloadKey{{Namespace: "ecommerce", Name: "cache"}, {Namespace: "ecommerce", Name: "auth"}},
			IsReady:      false,
			IsHibernate:  true,
		},
		{
			Key:          WorkloadKey{Namespace: "ecommerce", Name: "cache"},
			Dependencies: []WorkloadKey{{Namespace: "ecommerce", Name: "db"}},
			IsReady:      false,
			IsHibernate:  true,
		},
		{
			Key:          WorkloadKey{Namespace: "ecommerce", Name: "auth"},
			Dependencies: []WorkloadKey{{Namespace: "ecommerce", Name: "db"}},
			IsReady:      false,
			IsHibernate:  true,
		},
		{
			Key:          WorkloadKey{Namespace: "ecommerce", Name: "db"},
			Dependencies: nil,
			IsReady:      false,
			IsHibernate:  true,
		},
	}

	g := BuildGraph(workloads)
	plan, err := g.ResolveRestorationStages(WorkloadKey{Namespace: "ecommerce", Name: "app"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plan.Stages) != 3 {
		t.Fatalf("expected 3 stages, got %d", len(plan.Stages))
	}

	// Stage 0: db
	if len(plan.Stages[0]) != 1 || plan.Stages[0][0].Name != "db" {
		t.Errorf("expected stage 0 to be db, got %v", plan.Stages[0])
	}
	// Stage 1: [auth, cache] (in parallel)
	if len(plan.Stages[1]) != 2 {
		t.Fatalf("expected stage 1 to have 2 workloads (parallel), got %d", len(plan.Stages[1]))
	}
	// Stage 2: app
	if len(plan.Stages[2]) != 1 || plan.Stages[2][0].Name != "app" {
		t.Errorf("expected stage 2 to be app, got %v", plan.Stages[2])
	}
}

func TestSkipAlreadyReadyDependencies(t *testing.T) {
	// Backend -> [Postgres (Running & Ready), Redis (Hibernated)]
	workloads := []WorkloadInfo{
		{
			Key:          WorkloadKey{Namespace: "ecommerce", Name: "backend"},
			Dependencies: []WorkloadKey{{Namespace: "ecommerce", Name: "postgres"}, {Namespace: "ecommerce", Name: "redis"}},
			IsReady:      false,
			IsHibernate:  true,
		},
		{
			Key:          WorkloadKey{Namespace: "ecommerce", Name: "postgres"},
			Dependencies: nil,
			IsReady:      true,
			IsHibernate:  false,
		},
		{
			Key:          WorkloadKey{Namespace: "ecommerce", Name: "redis"},
			Dependencies: nil,
			IsReady:      false,
			IsHibernate:  true,
		},
	}

	g := BuildGraph(workloads)
	plan, err := g.ResolveRestorationStages(WorkloadKey{Namespace: "ecommerce", Name: "backend"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Stage 0: only redis (postgres is skipped because it's already ready)
	// Stage 1: backend
	if len(plan.Stages) != 2 {
		t.Fatalf("expected 2 stages, got %d", len(plan.Stages))
	}
	if len(plan.Stages[0]) != 1 || plan.Stages[0][0].Name != "redis" {
		t.Errorf("expected stage 0 to be redis, got %v", plan.Stages[0])
	}
	if len(plan.Stages[1]) != 1 || plan.Stages[1][0].Name != "backend" {
		t.Errorf("expected stage 1 to be backend, got %v", plan.Stages[1])
	}
}

func TestCyclicDependencyRecovery(t *testing.T) {
	// Circular: A -> B -> A
	workloads := []WorkloadInfo{
		{
			Key:          WorkloadKey{Namespace: "ecommerce", Name: "service-a"},
			Dependencies: []WorkloadKey{{Namespace: "ecommerce", Name: "service-b"}},
			IsReady:      false,
			IsHibernate:  true,
		},
		{
			Key:          WorkloadKey{Namespace: "ecommerce", Name: "service-b"},
			Dependencies: []WorkloadKey{{Namespace: "ecommerce", Name: "service-a"}},
			IsReady:      false,
			IsHibernate:  true,
		},
	}

	g := BuildGraph(workloads)
	plan, err := g.ResolveRestorationStages(WorkloadKey{Namespace: "ecommerce", Name: "service-a"})
	if err != nil {
		t.Fatalf("unexpected error on cyclic dependency: %v", err)
	}

	// Should group both in a single parallel stage instead of deadlocking
	if len(plan.Stages) != 1 {
		t.Fatalf("expected 1 parallel stage for cycle, got %d", len(plan.Stages))
	}
	if len(plan.Stages[0]) != 2 {
		t.Errorf("expected both cyclic services to be restored in parallel, got %v", plan.Stages[0])
	}
}

func TestTargetAlreadyReady(t *testing.T) {
	workloads := []WorkloadInfo{
		{
			Key:          WorkloadKey{Namespace: "ecommerce", Name: "ready-svc"},
			Dependencies: nil,
			IsReady:      true,
			IsHibernate:  false,
		},
	}

	g := BuildGraph(workloads)
	plan, err := g.ResolveRestorationStages(WorkloadKey{Namespace: "ecommerce", Name: "ready-svc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Stages) != 0 {
		t.Errorf("expected 0 stages for already ready service, got %d", len(plan.Stages))
	}
}
