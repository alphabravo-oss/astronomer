// dexconfigcheck validates a prepared Dex runtime document without ever
// printing its credential-bearing content. It is shipped in astronomer-shell
// and used by the Helm preflight hook before a Secret-volume cutover.
package main

import (
	"flag"
	"io"
	"os"

	"github.com/alphabravocompany/astronomer-go/internal/dexconfig"
)

const version = "2"

func main() {
	maxBytes := flag.Int64("max-bytes", 1<<20, "maximum decoded YAML bytes")
	showVersion := flag.Bool("version", false, "print validator contract version")
	flag.Parse()
	if *showVersion {
		_, _ = io.WriteString(os.Stdout, version+"\n")
		return
	}
	if *maxBytes < 1 || *maxBytes > 16<<20 {
		fail("invalid size bound")
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, *maxBytes+1))
	if err != nil || int64(len(raw)) > *maxBytes {
		fail("Dex configuration exceeds the bounded input size")
	}
	if err := dexconfig.ValidateRuntimeYAML(raw, *maxBytes); err != nil {
		// The shared validator's messages contain field names and reasons only;
		// never echo raw YAML or a decoded value.
		fail(err.Error())
	}
}

func fail(message string) {
	_, _ = io.WriteString(os.Stderr, "dexconfigcheck: "+message+"\n")
	os.Exit(1)
}
