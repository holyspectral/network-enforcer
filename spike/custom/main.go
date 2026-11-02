package main

import (
	"fmt"
	"os"
	"time"

	"spike/common"
)

func factory(glob, state string, onLine common.Emit) (func(), error) {
	t, err := startTailer(glob, state, 200*time.Millisecond, onLine)
	if err != nil {
		return nil, err
	}
	return t.Stop, nil
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
