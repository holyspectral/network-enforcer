package main

import (
	"fmt"
	"os"
	"time"

	"spike/common"
)

func factory(glob, state string, onLine common.Emit) (func(), error) {
	t, err := startTailer(glob, state, 200*time.Millisecond, "read_new", onLine)
	if err != nil {
		return nil, err
	}
	return func() {
		if err := t.Stop(); err != nil {
			fmt.Fprintln(os.Stderr, "stop:", err)
		}
	}, nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "export" {
		common.RunExport(factory)
		return
	}
	rate := common.ParseRate(os.Args)
	res, err := common.Bench(factory, rate, 20*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("%+v\n", res)
}
