package main

import (
	"github.com/d1ys3nk0/sfm/internal/sfm"
	"os"
)

var version = "dev"
var commit = "unknown"

func main() {
	sfm.Version = version
	sfm.Commit = commit
	os.Exit(sfm.Run(os.Args[1:], os.Stdout, os.Stderr))
}
