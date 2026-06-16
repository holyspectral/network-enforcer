package e2e_test

import (
	"context"
	"fmt"

	"sigs.k8s.io/e2e-framework/pkg/env"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
)

type cniType string

const (
	kindnet cniType = "kindnet"
	calico  cniType = "calico"
	cilium  cniType = "cilium"
)

func installCilium(ctx context.Context, _ *envconf.Config) (context.Context, error) {
	// todo!: Install cilium CNI
	return ctx, nil
}

func installCalico(ctx context.Context, _ *envconf.Config) (context.Context, error) {
	// todo!: Install calico CNI
	return ctx, nil
}

func installCNI(t cniType) env.Func {
	return func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
		switch t {
		case kindnet:
			// kindnet is already installed by kind, nothing to do
			return ctx, nil
		case calico:
			return installCalico(ctx, cfg)
		case cilium:
			return installCilium(ctx, cfg)
		default:
			return ctx, fmt.Errorf("unknown CNI type: %s", t)
		}
	}
}
