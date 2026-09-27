module github.com/sharedcode/joltrin/jsondb

go 1.26.8

replace github.com/sharedcode/joltrin/v5 => ../

replace github.com/sharedcode/joltrin/infs => ../infs

replace github.com/sharedcode/joltrin/adapters/redis => ../adapters/redis

replace github.com/sharedcode/joltrin/adapters/cassandra => ../adapters/cassandra

require (
	github.com/google/uuid v1.6.0
	github.com/sharedcode/joltrin/v5 v5.0.0
)

require (
	cel.dev/expr v0.25.2 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/goccy/go-json v0.9.11 // indirect
	github.com/google/cel-go v0.29.0 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/klauspost/reedsolomon v1.12.4 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/ncw/directio v1.0.5 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	github.com/sethvargo/go-retry v0.3.0 // indirect
	github.com/sharedcode/joltrin/infs v0.0.0 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/exp v0.0.0-20260410095643-746e56fc9e2f // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
)
