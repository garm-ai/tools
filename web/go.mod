module github.com/garm-ai/tools/web

go 1.26.0

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.12-20260825204119-511051f7f437.2
	github.com/garm-ai/garm v0.14.2
	github.com/garm-ai/tool-go v0.5.0
	github.com/garm-ai/tools/sanitize v0.1.1
	github.com/garm-ai/tools/taxonomy v0.1.1
	golang.org/x/net v0.59.0
	google.golang.org/protobuf v1.36.12
	gopkg.in/yaml.v3 v3.0.1
)

require golang.org/x/text v0.42.0 // indirect
