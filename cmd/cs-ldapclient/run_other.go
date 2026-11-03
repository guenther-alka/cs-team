//go:build !windows && !linux && !freebsd && !darwin

package main

import "fmt"

func uiLang() string { return "en" }

func run(o options) int {
	fmt.Println("cs-ldapclient: this operating system is not supported.")
	return 1
}
