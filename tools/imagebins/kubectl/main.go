// Command kubectl is upstream kubectl (k8s.io/kubernetes/cmd/kubectl/kubectl.go,
// verbatim main) compiled with the repository's pinned Go toolchain so the
// astronomer-shell image never ships a binary built by an older Go release.
package main

import (
	"k8s.io/component-base/cli"
	"k8s.io/kubectl/pkg/cmd"
	"k8s.io/kubectl/pkg/cmd/util"
)

func main() {
	command := cmd.NewDefaultKubectlCommand()
	if err := cli.RunNoErrOutput(command); err != nil {
		util.CheckErr(err)
	}
}
