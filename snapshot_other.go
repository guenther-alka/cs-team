//go:build !windows

package main

func volumeLabel(string) string { return "" }
