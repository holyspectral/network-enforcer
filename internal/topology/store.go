package topology

import (
	"sync"
	"time"
)

type Store struct {
	mu    sync.RWMutex
	flows map[flowKey]*FlowRecord
}

func NewStore() *Store {
	return &Store{
		flows: make(map[flowKey]*FlowRecord),
	}
}

func (s *Store) Record(f FlowRecord) {
	key := flowKey{
		Source:   f.Source,
		Dest:     f.Dest,
		DstPort:  f.DstPort,
		Protocol: f.Protocol,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.flows[key]; ok {
		existing.LastSeen = f.LastSeen
		existing.ByteCount += f.ByteCount
		if f.SrcAddress != "" {
			existing.SrcAddress = f.SrcAddress
		}
		if f.DstAddress != "" {
			existing.DstAddress = f.DstAddress
		}
	} else {
		stored := f
		s.flows[key] = &stored
	}
}

func (s *Store) FlowsForWorkload(wk WorkloadKey) []FlowRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []FlowRecord
	for _, f := range s.flows {
		if f.Source == wk || f.Dest == wk {
			result = append(result, *f)
		}
	}
	return result
}

func (s *Store) Workloads() []WorkloadKey {
	s.mu.RLock()
	defer s.mu.RUnlock()

	seen := make(map[WorkloadKey]struct{})
	for _, f := range s.flows {
		if f.Source.OwnerName != "" {
			seen[f.Source] = struct{}{}
		}
		if f.Dest.OwnerName != "" {
			seen[f.Dest] = struct{}{}
		}
	}

	result := make([]WorkloadKey, 0, len(seen))
	for wk := range seen {
		result = append(result, wk)
	}
	return result
}

func (s *Store) Prune(cutoff time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for key, f := range s.flows {
		if f.LastSeen.Before(cutoff) {
			delete(s.flows, key)
		}
	}
}
