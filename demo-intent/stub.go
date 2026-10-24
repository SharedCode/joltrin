//go:build !(js && wasm)

// This package only does anything under GOOS=js GOARCH=wasm. The stub keeps
// go build, go vet and go test ./... working on every other platform without
// adding the package to the exclusion lists in CI.
package main

func main() {}
