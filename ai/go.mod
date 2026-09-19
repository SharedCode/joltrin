module github.com/sharedcode/joltrin/ai

go 1.26.8

replace github.com/sharedcode/joltrin => ../

replace github.com/sharedcode/joltrin/infs => ../infs

replace github.com/sharedcode/joltrin/jsondb => ../jsondb

replace github.com/sharedcode/joltrin/search => ../search

require (
	github.com/google/cel-go v0.30.0
	github.com/google/uuid v1.6.0
	github.com/kelindar/search v0.4.1
	github.com/sethvargo/go-retry v0.3.0
	github.com/sharedcode/joltrin v0.0.0
	github.com/sharedcode/joltrin/infs v0.0.0
	github.com/sharedcode/joltrin/jsondb v0.0.0
	github.com/sharedcode/joltrin/search v0.0.0
	github.com/stretchr/testify v1.11.1
	github.com/yuin/goldmark v1.8.2
	golang.org/x/sync v0.22.0
)

require (
	cel.dev/expr v0.25.2 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/ebitengine/purego v0.8.1 // indirect
	github.com/goccy/go-json v0.9.11 // indirect
	github.com/kelindar/iostream v1.4.0 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/klauspost/reedsolomon v1.12.4 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/ncw/directio v1.0.5 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/exp v0.0.0-20260410095643-746e56fc9e2f // indirect
	golang.org/x/sys v0.47.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
