module github.com/nickolaev/pi-mono/packages/go-pi-agent

go 1.24.7

require github.com/nickolaev/pi-mono/packages/go-pi-ai v0.0.0

require (
	golang.org/x/net v0.48.0 // indirect
	golang.org/x/sys v0.39.0 // indirect
	golang.org/x/text v0.32.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20251202230838-ff82c1b0f217 // indirect
	google.golang.org/grpc v1.79.1 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/nickolaev/pi-mono/packages/go-pi-ai => ../go-pi-ai
