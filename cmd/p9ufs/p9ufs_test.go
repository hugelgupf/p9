package main

import (
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

func TestCLI(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir: "testscripts",
	})
}

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"p9ufs": main,
	})
}
