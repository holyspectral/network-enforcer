package fingerprint

import (
	"testing"
	"time"

	"secuity.rancher.io/network-enforcer/internal/topology"
)

func TestGenerate_IngressAndEgress(t *testing.T) {
	wk := topology.WorkloadKey{Namespace: "default", OwnerKind: "Deployment", OwnerName: "app"}
	now := time.Now()

	flows := []topology.FlowRecord{
		{
			Source:    topology.WorkloadKey{Namespace: "default", OwnerKind: "Deployment", OwnerName: "client"},
			Dest:      wk,
			DstPort:   8080,
			Protocol:  "TCP",
			FirstSeen: now,
			LastSeen:  now,
		},
		{
			Source:    wk,
			Dest:      topology.WorkloadKey{Namespace: "default", OwnerKind: "Deployment", OwnerName: "db"},
			DstPort:   5432,
			Protocol:  "TCP",
			FirstSeen: now,
			LastSeen:  now,
		},
	}

	ingress, egress := Generate(wk, flows)

	if len(ingress) != 1 {
		t.Fatalf("expected 1 ingress rule, got %d", len(ingress))
	}
	if len(egress) != 1 {
		t.Fatalf("expected 1 egress rule, got %d", len(egress))
	}

	// Verify ingress
	if ingress[0].Peers[0].Workload == nil || ingress[0].Peers[0].Workload.Name != "client" {
		t.Fatalf("expected ingress peer 'client', got %+v", ingress[0].Peers[0])
	}
	if ingress[0].Ports[0].Port != 8080 {
		t.Fatalf("expected ingress port 8080, got %d", ingress[0].Ports[0].Port)
	}

	// Verify egress
	if egress[0].Peers[0].Workload == nil || egress[0].Peers[0].Workload.Name != "db" {
		t.Fatalf("expected egress peer 'db', got %+v", egress[0].Peers[0])
	}
	if egress[0].Ports[0].Port != 5432 {
		t.Fatalf("expected egress port 5432, got %d", egress[0].Ports[0].Port)
	}
}

func TestGenerate_ExternalPeer(t *testing.T) {
	wk := topology.WorkloadKey{Namespace: "default", OwnerKind: "Deployment", OwnerName: "app"}
	now := time.Now()

	flows := []topology.FlowRecord{
		{
			Source:     topology.WorkloadKey{},
			Dest:       wk,
			DstPort:    443,
			Protocol:   "TCP",
			SrcAddress: "203.0.113.5",
			FirstSeen:  now,
			LastSeen:   now,
		},
	}

	ingress, _ := Generate(wk, flows)

	if len(ingress) != 1 {
		t.Fatalf("expected 1 ingress rule, got %d", len(ingress))
	}
	if ingress[0].Peers[0].CIDR != "203.0.113.5/32" {
		t.Fatalf("expected CIDR 203.0.113.5/32, got %s", ingress[0].Peers[0].CIDR)
	}
}

func TestGenerate_MultiplePorts(t *testing.T) {
	wk := topology.WorkloadKey{Namespace: "default", OwnerKind: "Deployment", OwnerName: "app"}
	peer := topology.WorkloadKey{Namespace: "default", OwnerKind: "Deployment", OwnerName: "client"}
	now := time.Now()

	flows := []topology.FlowRecord{
		{Source: peer, Dest: wk, DstPort: 80, Protocol: "TCP", FirstSeen: now, LastSeen: now},
		{Source: peer, Dest: wk, DstPort: 443, Protocol: "TCP", FirstSeen: now, LastSeen: now},
	}

	ingress, _ := Generate(wk, flows)

	if len(ingress) != 1 {
		t.Fatalf("expected 1 ingress rule (same peer), got %d", len(ingress))
	}
	if len(ingress[0].Ports) != 2 {
		t.Fatalf("expected 2 ports, got %d", len(ingress[0].Ports))
	}
	// Should be sorted: 80, 443
	if ingress[0].Ports[0].Port != 80 || ingress[0].Ports[1].Port != 443 {
		t.Fatalf("expected ports [80, 443], got [%d, %d]", ingress[0].Ports[0].Port, ingress[0].Ports[1].Port)
	}
}

func TestGenerate_NoFlows(t *testing.T) {
	wk := topology.WorkloadKey{Namespace: "default", OwnerKind: "Deployment", OwnerName: "app"}
	ingress, egress := Generate(wk, nil)
	if ingress != nil || egress != nil {
		t.Fatal("expected nil rules for no flows")
	}
}

func TestGenerate_CrossNamespace(t *testing.T) {
	wk := topology.WorkloadKey{Namespace: "backend", OwnerKind: "Deployment", OwnerName: "api"}
	now := time.Now()

	flows := []topology.FlowRecord{
		{
			Source:    topology.WorkloadKey{Namespace: "frontend", OwnerKind: "Deployment", OwnerName: "web"},
			Dest:      wk,
			DstPort:   8080,
			Protocol:  "TCP",
			FirstSeen: now,
			LastSeen:  now,
		},
	}

	ingress, _ := Generate(wk, flows)

	if len(ingress) != 1 {
		t.Fatalf("expected 1 ingress rule, got %d", len(ingress))
	}
	if ingress[0].Peers[0].Namespace != "frontend" {
		t.Fatalf("expected namespace 'frontend', got %s", ingress[0].Peers[0].Namespace)
	}
}
