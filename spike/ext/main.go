package main

import (
	"fmt"
	"os"
	"time"

	"spike/common"
)

func main() {
	if os.Args[1] == "noop" {
		// Measures the line generator alone, so Go-process CPU can be corrected.
		rate := common.ParseRate(os.Args[1:])
		res, err := common.Bench(
			func(string, string, common.Emit) (func(), error) { return func() {}, nil },
			rate,
			20*time.Second,
		)
		fmt.Printf("noop %+v %v\n", res, err)
		return
	}
	t, ok := tools[os.Args[1]]
	if !ok {
		fmt.Fprintln(os.Stderr, "unknown tool")
		os.Exit(1)
	}
	rate := common.ParseRate(os.Args[1:])
	res, err := common.BenchWith(factory(t), rate, 20*time.Second, probe)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("%s %+v\n", t.name, res)
}
