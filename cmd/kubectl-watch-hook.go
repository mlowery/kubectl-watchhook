package main

import (
	"os"

	"github.com/spf13/pflag"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/mlowery/kubectl-watchhook/pkg/cmd"
)

func main() {
	flags := pflag.NewFlagSet("kubectl-watchhook", pflag.ExitOnError)
	pflag.CommandLine = flags

	root := cmd.NewCmdWatchHook(genericclioptions.IOStreams{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr})
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
