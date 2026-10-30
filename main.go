package main

import (
	"log"
	"os"
	"strings"

	"github.com/psd-coder/agentdiff/internal/app"
	"github.com/psd-coder/agentdiff/internal/gitx"
)

// startPath is the repository to open: the first argument, or the working
// directory when started from a terminal inside a repository. Only an
// argument that is not a repository is an error worth showing.
func startPath() string {
	for _, arg := range os.Args[1:] {
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	wd, err := os.Getwd()
	if err != nil || wd == "/" {
		return ""
	}
	if _, err := gitx.Open(wd); err != nil {
		return ""
	}
	return wd
}

func main() {
	if err := app.Run(startPath()); err != nil {
		log.Fatal(err)
	}
}
