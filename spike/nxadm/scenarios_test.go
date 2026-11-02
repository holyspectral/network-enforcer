package main

import (
	"testing"

	"spike/common"
)

func TestScenarios(t *testing.T) { common.RunScenarios(t, factory) }
