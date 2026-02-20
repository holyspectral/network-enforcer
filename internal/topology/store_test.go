package topology

import (
	"sync"
	"testing"
	"time"
)

func TestStore_Record_NewFlow(t *testing.T) {
	s := NewStore()
	now := time.Now()

	f := FlowRecord{
		Source:    WorkloadKey{Namespace: "default", OwnerKind: "Deployment", OwnerName: "frontend"},
		Dest:      WorkloadKey{Namespace: "default", OwnerKind: "Deployment", OwnerName: "backend"},
		DstPort:   8080,
		Protocol:  "TCP",
		FirstSeen: now,
		LastSeen:  now,
		ByteCount: 100,
	}
	s.Record(f)

	flows := s.FlowsForWorkload(f.Source)
	if len(flows) != 1 {
		t.Fatalf("expected 1 flow, got %d", len(flows))
	}
	if flows[0].ByteCount != 100 {
		t.Fatalf("expected 100 bytes, got %d", flows[0].ByteCount)
	}
}

func TestStore_Record_Upsert(t *testing.T) {
	s := NewStore()
	now := time.Now()

	base := FlowRecord{
		Source:    WorkloadKey{Namespace: "default", OwnerKind: "Deployment", OwnerName: "frontend"},
		Dest:      WorkloadKey{Namespace: "default", OwnerKind: "Deployment", OwnerName: "backend"},
		DstPort:   8080,
		Protocol:  "TCP",
		FirstSeen: now,
		LastSeen:  now,
		ByteCount: 100,
	}
	s.Record(base)

	later := now.Add(time.Minute)
	base.LastSeen = later
	base.ByteCount = 50
	s.Record(base)

	flows := s.FlowsForWorkload(base.Source)
	if len(flows) != 1 {
		t.Fatalf("expected 1 flow after upsert, got %d", len(flows))
	}
	if flows[0].ByteCount != 150 {
		t.Fatalf("expected 150 bytes accumulated, got %d", flows[0].ByteCount)
	}
	if !flows[0].LastSeen.Equal(later) {
		t.Fatalf("expected lastSeen updated to %v, got %v", later, flows[0].LastSeen)
	}
}

func TestStore_FlowsForWorkload_BothDirections(t *testing.T) {
	s := NewStore()
	now := time.Now()

	wk := WorkloadKey{Namespace: "default", OwnerKind: "Deployment", OwnerName: "app"}

	// Flow where app is source
	s.Record(FlowRecord{
		Source:    wk,
		Dest:      WorkloadKey{Namespace: "default", OwnerKind: "Deployment", OwnerName: "db"},
		DstPort:   5432,
		Protocol:  "TCP",
		FirstSeen: now,
		LastSeen:  now,
		ByteCount: 100,
	})

	// Flow where app is destination
	s.Record(FlowRecord{
		Source:    WorkloadKey{Namespace: "default", OwnerKind: "Deployment", OwnerName: "client"},
		Dest:      wk,
		DstPort:   8080,
		Protocol:  "TCP",
		FirstSeen: now,
		LastSeen:  now,
		ByteCount: 200,
	})

	flows := s.FlowsForWorkload(wk)
	if len(flows) != 2 {
		t.Fatalf("expected 2 flows, got %d", len(flows))
	}
}

func TestStore_Workloads(t *testing.T) {
	s := NewStore()
	now := time.Now()

	s.Record(FlowRecord{
		Source:    WorkloadKey{Namespace: "ns1", OwnerKind: "Deployment", OwnerName: "a"},
		Dest:      WorkloadKey{Namespace: "ns2", OwnerKind: "StatefulSet", OwnerName: "b"},
		DstPort:   80,
		Protocol:  "TCP",
		FirstSeen: now,
		LastSeen:  now,
		ByteCount: 1,
	})

	workloads := s.Workloads()
	if len(workloads) != 2 {
		t.Fatalf("expected 2 workloads, got %d", len(workloads))
	}
}

func TestStore_Workloads_ExcludesEmpty(t *testing.T) {
	s := NewStore()
	now := time.Now()

	// External peer (no owner name)
	s.Record(FlowRecord{
		Source:     WorkloadKey{},
		Dest:       WorkloadKey{Namespace: "default", OwnerKind: "Deployment", OwnerName: "app"},
		DstPort:    443,
		Protocol:   "TCP",
		SrcAddress: "1.2.3.4",
		FirstSeen:  now,
		LastSeen:   now,
		ByteCount:  1,
	})

	workloads := s.Workloads()
	if len(workloads) != 1 {
		t.Fatalf("expected 1 workload (external excluded), got %d", len(workloads))
	}
}

func TestStore_Prune(t *testing.T) {
	s := NewStore()
	old := time.Now().Add(-time.Hour)
	recent := time.Now()

	s.Record(FlowRecord{
		Source:    WorkloadKey{Namespace: "default", OwnerKind: "Deployment", OwnerName: "old"},
		Dest:      WorkloadKey{Namespace: "default", OwnerKind: "Deployment", OwnerName: "target"},
		DstPort:   80,
		Protocol:  "TCP",
		FirstSeen: old,
		LastSeen:  old,
		ByteCount: 1,
	})
	s.Record(FlowRecord{
		Source:    WorkloadKey{Namespace: "default", OwnerKind: "Deployment", OwnerName: "new"},
		Dest:      WorkloadKey{Namespace: "default", OwnerKind: "Deployment", OwnerName: "target"},
		DstPort:   80,
		Protocol:  "TCP",
		FirstSeen: recent,
		LastSeen:  recent,
		ByteCount: 1,
	})

	cutoff := time.Now().Add(-30 * time.Minute)
	s.Prune(cutoff)

	workloads := s.Workloads()
	found := false
	for _, w := range workloads {
		if w.OwnerName == "old" {
			t.Fatal("expected 'old' workload to be pruned")
		}
		if w.OwnerName == "new" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected 'new' workload to survive pruning")
	}
}

func TestStore_ConcurrentAccess(t *testing.T) {
	s := NewStore()
	now := time.Now()

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.Record(FlowRecord{
				Source:    WorkloadKey{Namespace: "default", OwnerKind: "Deployment", OwnerName: "src"},
				Dest:      WorkloadKey{Namespace: "default", OwnerKind: "Deployment", OwnerName: "dst"},
				DstPort:   int32(8080 + i%10),
				Protocol:  "TCP",
				FirstSeen: now,
				LastSeen:  now,
				ByteCount: 1,
			})
		}(i)
	}

	for range 50 {
		wg.Go(func() {
			_ = s.FlowsForWorkload(WorkloadKey{Namespace: "default", OwnerKind: "Deployment", OwnerName: "src"})
			_ = s.Workloads()
		})
	}

	wg.Wait()
}
