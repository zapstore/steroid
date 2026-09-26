package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "enrich":
		os.Exit(enrich(os.Args[2:]))
	case "serve":
		os.Exit(serve(os.Args[2:]))
	case "seal":
		os.Exit(seal(os.Args[2:]))
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: steroid enrich [--skip-analysis] [--debug debug.txt] <app-id>\n")
	fmt.Fprintf(os.Stderr, "       steroid serve\n")
	fmt.Fprintf(os.Stderr, "       steroid seal [--skip-analysis]\n")
}
