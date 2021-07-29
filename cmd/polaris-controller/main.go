package main

import (
	"fmt"
	"github.com/polarismesh/polaris-controller/cmd/polaris-controller/app"
	"k8s.io/component-base/logs"
	"math/rand"
	"os"
	"time"
)

func main() {
	rand.Seed(time.Now().UnixNano())

	command := app.NewPolarisControllerManagerCommand()
	logs.InitLogs()
	defer logs.FlushLogs()
	if err := command.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
