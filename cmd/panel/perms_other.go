//go:build !unix

package main

func restrictUmask() {}

func secureDataDir(string) error { return nil }
