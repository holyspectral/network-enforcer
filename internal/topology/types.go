package topology

import "time"

type WorkloadKey struct {
	Namespace string
	OwnerKind string
	OwnerName string
}

type FlowRecord struct {
	Source   WorkloadKey
	Dest     WorkloadKey
	DstPort  int32
	Protocol string // TCP or UDP

	SrcAddress string
	DstAddress string

	FirstSeen time.Time
	LastSeen  time.Time
	ByteCount int64
}

type flowKey struct {
	Source   WorkloadKey
	Dest     WorkloadKey
	DstPort  int32
	Protocol string
}
