package fingerprint

import (
	"fmt"
	"sort"

	securityv1alpha1 "secuity.rancher.io/network-enforcer/api/v1alpha1"
	"secuity.rancher.io/network-enforcer/internal/topology"
)

type peerKey struct {
	Namespace string
	OwnerKind string
	OwnerName string
	CIDR      string
}

type portKey struct {
	Protocol string
	Port     int32
}

func Generate(wk topology.WorkloadKey, flows []topology.FlowRecord) (ingress, egress []securityv1alpha1.ProposedRule) {
	ingressPeers := make(map[peerKey]map[portKey]struct{})
	egressPeers := make(map[peerKey]map[portKey]struct{})

	for _, f := range flows {
		if f.Dest == wk {
			pk := makePeerKey(f.Source, f.SrcAddress)
			if ingressPeers[pk] == nil {
				ingressPeers[pk] = make(map[portKey]struct{})
			}
			ingressPeers[pk][portKey{Protocol: f.Protocol, Port: f.DstPort}] = struct{}{}
		}
		if f.Source == wk {
			pk := makePeerKey(f.Dest, f.DstAddress)
			if egressPeers[pk] == nil {
				egressPeers[pk] = make(map[portKey]struct{})
			}
			egressPeers[pk][portKey{Protocol: f.Protocol, Port: f.DstPort}] = struct{}{}
		}
	}

	ingress = buildRules(ingressPeers)
	egress = buildRules(egressPeers)
	return
}

func makePeerKey(wk topology.WorkloadKey, address string) peerKey {
	if wk.OwnerName == "" {
		return peerKey{CIDR: fmt.Sprintf("%s/32", address)}
	}
	return peerKey{Namespace: wk.Namespace, OwnerKind: wk.OwnerKind, OwnerName: wk.OwnerName}
}

func buildRules(peers map[peerKey]map[portKey]struct{}) []securityv1alpha1.ProposedRule {
	sortedPeers := make([]peerKey, 0, len(peers))
	for pk := range peers {
		sortedPeers = append(sortedPeers, pk)
	}
	sort.Slice(sortedPeers, func(i, j int) bool {
		a, b := sortedPeers[i], sortedPeers[j]
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.OwnerKind != b.OwnerKind {
			return a.OwnerKind < b.OwnerKind
		}
		if a.OwnerName != b.OwnerName {
			return a.OwnerName < b.OwnerName
		}
		return a.CIDR < b.CIDR
	})

	if len(sortedPeers) == 0 {
		return nil
	}

	rules := make([]securityv1alpha1.ProposedRule, 0, len(sortedPeers))
	for _, pk := range sortedPeers {
		var peer securityv1alpha1.PolicyPeer
		if pk.CIDR != "" {
			peer.CIDR = pk.CIDR
		} else {
			peer.Workload = &securityv1alpha1.WorkloadReference{
				Kind: pk.OwnerKind,
				Name: pk.OwnerName,
			}
			peer.Namespace = pk.Namespace
		}

		ports := make([]securityv1alpha1.PortRule, 0, len(peers[pk]))
		for p := range peers[pk] {
			ports = append(ports, securityv1alpha1.PortRule{
				Protocol: p.Protocol,
				Port:     p.Port,
			})
		}
		sort.Slice(ports, func(i, j int) bool {
			if ports[i].Port != ports[j].Port {
				return ports[i].Port < ports[j].Port
			}
			return ports[i].Protocol < ports[j].Protocol
		})

		rules = append(rules, securityv1alpha1.ProposedRule{
			Peers: []securityv1alpha1.PolicyPeer{peer},
			Ports: ports,
		})
	}

	return rules
}
