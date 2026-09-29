package main

import (
	"fmt"
	"os"
)

// version is the build ref, set with -X main.version=...
var version = "dev"

func main() {
	if len(os.Args) >= 2 && (os.Args[1] == "-v" || os.Args[1] == "--version") {
		fmt.Println(version)
		return
	}
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "enrich":
		os.Exit(enrich(os.Args[2:]))
	case "serve":
		os.Exit(serve(os.Args[2:]))
	case "bundle":
		os.Exit(bundle(os.Args[2:]))
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: steroid -v\n")
	fmt.Fprintf(os.Stderr, "       steroid enrich --filter app_id [--debug] [--force]\n")
	fmt.Fprintf(os.Stderr, "       steroid serve\n")
	fmt.Fprintf(os.Stderr, "       steroid bundle [--filter app_id] [--no-enrich] [--debug]\n")
}
