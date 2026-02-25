package agent

import (
	"context"
	"fmt"
	"net"
	"sync"

	"google.golang.org/grpc"

	"github.com/nickolaev/pi-mono/packages/go-pi-agent/extpb"
)

// ExtensionErrorHandler is called when an extension encounters an error
// during hook dispatch or tool execution. This enables observability
// instead of silently swallowing errors.
type ExtensionErrorHandler func(extensionID string, operation string, err error)

// ExtensionLogHandler is called when an extension sends a log message
// via the HostService.Log RPC.
type ExtensionLogHandler func(extensionID string, level string, message string, fields map[string]any)

// hostServiceServer implements the HostService gRPC server on the host side.
// It handles SetState and Log callbacks from gRPC extension processes.
type hostServiceServer struct {
	extpb.UnimplementedHostServiceServer

	mu          sync.Mutex
	ext         *Extension
	onLog       ExtensionLogHandler
	onError     ExtensionErrorHandler
}

func (h *hostServiceServer) SetState(_ context.Context, req *extpb.StateSetRequest) (*extpb.Empty, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.ext.State == nil {
		h.ext.State = make(map[string]any)
	}

	var value any
	if req.Value != nil {
		value = req.Value.AsInterface()
	}
	h.ext.State[req.Key] = value

	return &extpb.Empty{}, nil
}

func (h *hostServiceServer) Log(_ context.Context, req *extpb.LogRequest) (*extpb.Empty, error) {
	if h.onLog != nil {
		var fields map[string]any
		if req.Fields != nil {
			fields = req.Fields.AsMap()
		}
		h.onLog(h.ext.Manifest.ID, req.Level, req.Message, fields)
	}
	return &extpb.Empty{}, nil
}

// hostGRPCServer manages a HostService gRPC server that extensions dial back to.
type hostGRPCServer struct {
	server   *grpc.Server
	listener net.Listener
	addr     string
}

// startHostServiceServer starts a gRPC server for HostService on a random port.
func startHostServiceServer(ext *Extension, onLog ExtensionLogHandler, onError ExtensionErrorHandler) (*hostGRPCServer, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("host service listen: %w", err)
	}

	server := grpc.NewServer()
	extpb.RegisterHostServiceServer(server, &hostServiceServer{
		ext:     ext,
		onLog:   onLog,
		onError: onError,
	})

	go server.Serve(listener)

	return &hostGRPCServer{
		server:   server,
		listener: listener,
		addr:     listener.Addr().String(),
	}, nil
}

func (h *hostGRPCServer) Stop() {
	if h.server != nil {
		h.server.GracefulStop()
	}
}
