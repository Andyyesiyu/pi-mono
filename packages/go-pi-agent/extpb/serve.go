package extpb

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"
)

// HandshakeVersion is the current protocol version for the handshake line.
const HandshakeVersion = "1"

// Serve starts a gRPC server for the given ExtensionServiceServer and performs
// the handshake protocol with the host process.
//
// The handshake protocol writes a single line to stdout:
//
//	PLUGIN|<version>|tcp|<address>|grpc
//
// The host reads this line, parses the address, and dials the gRPC connection.
//
// This function blocks until SIGTERM/SIGINT or the host closes stdin.
//
// Usage in an extension:
//
//	type MyExtension struct {
//	    extpb.UnimplementedExtensionServiceServer
//	}
//
//	func (m *MyExtension) Initialize(ctx context.Context, req *extpb.InitializeRequest) (*extpb.InitializeResponse, error) {
//	    return &extpb.InitializeResponse{
//	        Hooks: []string{"tool_call"},
//	    }, nil
//	}
//
//	func main() {
//	    extpb.Serve(&MyExtension{})
//	}
func Serve(impl ExtensionServiceServer, opts ...ServeOption) error {
	cfg := &serveConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	// Listen on a random port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	// Create gRPC server.
	var grpcOpts []grpc.ServerOption
	if cfg.maxRecvMsgSize > 0 {
		grpcOpts = append(grpcOpts, grpc.MaxRecvMsgSize(cfg.maxRecvMsgSize))
	}
	if cfg.maxSendMsgSize > 0 {
		grpcOpts = append(grpcOpts, grpc.MaxSendMsgSize(cfg.maxSendMsgSize))
	}

	server := grpc.NewServer(grpcOpts...)
	RegisterExtensionServiceServer(server, impl)

	// If the implementation also provides HostService callbacks, register on a
	// separate connection later. For now, we only serve the extension side.

	// Print handshake line to stdout.
	// Format: PLUGIN|<version>|tcp|<address>|grpc
	fmt.Fprintf(os.Stdout, "PLUGIN|%s|tcp|%s|grpc\n", HandshakeVersion, listener.Addr().String())

	// Start serving in background.
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()

	// Wait for shutdown signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// Also watch stdin — if the host closes stdin, we should exit.
	stdinClosed := make(chan struct{})
	go func() {
		buf := make([]byte, 1)
		for {
			_, err := os.Stdin.Read(buf)
			if err != nil {
				close(stdinClosed)
				return
			}
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-sigCh:
		server.GracefulStop()
		return nil
	case <-stdinClosed:
		server.GracefulStop()
		return nil
	}
}

// ServeOption configures the Serve function.
type ServeOption func(*serveConfig)

type serveConfig struct {
	maxRecvMsgSize int
	maxSendMsgSize int
}

// WithMaxRecvMsgSize sets the maximum receive message size for gRPC.
func WithMaxRecvMsgSize(size int) ServeOption {
	return func(c *serveConfig) {
		c.maxRecvMsgSize = size
	}
}

// WithMaxSendMsgSize sets the maximum send message size for gRPC.
func WithMaxSendMsgSize(size int) ServeOption {
	return func(c *serveConfig) {
		c.maxSendMsgSize = size
	}
}
