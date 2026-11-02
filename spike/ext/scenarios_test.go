package main

import (
	"os"
	"testing"

	"spike/common"
)

func TestScenarios(t *testing.T) {
	name := os.Getenv("TOOL")
	tl, ok := tools[name]
	if !ok {
		t.Fatalf("set TOOL to one of vector, fluent-bit, otel")
	}
	common.RunScenarios(t, factory(tl))
}
