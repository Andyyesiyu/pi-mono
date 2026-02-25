package agent

import (
	"fmt"
	"sync"
	"time"
)

// SupervisorConfig controls auto-restart behavior for process extensions.
type SupervisorConfig struct {
	// MaxRestarts is the maximum number of restarts before giving up.
	// Zero means no auto-restart.
	MaxRestarts int

	// InitialBackoff is the first retry delay (doubles on each failure).
	InitialBackoff time.Duration

	// MaxBackoff caps the exponential backoff.
	MaxBackoff time.Duration

	// ResetAfter resets the restart counter if the process stays alive for this long.
	// This prevents a long-running extension from hitting max restarts after
	// infrequent crashes.
	ResetAfter time.Duration
}

// DefaultSupervisorConfig is the default supervision policy.
var DefaultSupervisorConfig = SupervisorConfig{
	MaxRestarts:    5,
	InitialBackoff: 1 * time.Second,
	MaxBackoff:     30 * time.Second,
	ResetAfter:     60 * time.Second,
}

// extensionSupervisor monitors and auto-restarts a process extension.
type extensionSupervisor struct {
	mu       sync.Mutex
	ext      *Extension
	config   SupervisorConfig
	onError  ExtensionErrorHandler
	restarts int
	stopped  bool
	done     chan struct{}
}

// newExtensionSupervisor creates a supervisor for an extension.
func newExtensionSupervisor(ext *Extension, config SupervisorConfig, onError ExtensionErrorHandler) *extensionSupervisor {
	return &extensionSupervisor{
		ext:     ext,
		config:  config,
		onError: onError,
		done:    make(chan struct{}),
	}
}

// Start begins monitoring the extension process. If the process crashes,
// it will be restarted with exponential backoff up to MaxRestarts.
func (s *extensionSupervisor) Start() {
	go s.watchLoop()
}

// Stop terminates the supervisor. Safe to call multiple times.
func (s *extensionSupervisor) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	close(s.done)
	s.mu.Unlock()
}

func (s *extensionSupervisor) watchLoop() {
	backoff := s.config.InitialBackoff

	for {
		// Wait for the process to exit.
		exitCh := s.waitForExit()
		if exitCh == nil {
			return
		}

		select {
		case <-exitCh:
			// Process exited.
		case <-s.done:
			return
		}

		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			return
		}

		s.restarts++
		if s.restarts > s.config.MaxRestarts {
			s.mu.Unlock()
			if s.onError != nil {
				s.onError(s.ext.Manifest.ID, "supervisor",
					fmt.Errorf("extension crashed %d times, giving up", s.restarts-1))
			}
			return
		}

		attempt := s.restarts
		s.mu.Unlock()

		if s.onError != nil {
			s.onError(s.ext.Manifest.ID, "supervisor",
				fmt.Errorf("extension crashed (attempt %d/%d), restarting in %s",
					attempt, s.config.MaxRestarts, backoff))
		}

		// Wait with backoff before restart.
		select {
		case <-time.After(backoff):
		case <-s.done:
			return
		}

		// Exponential backoff.
		backoff *= 2
		if backoff > s.config.MaxBackoff {
			backoff = s.config.MaxBackoff
		}

		// Attempt restart.
		if err := s.restartExtension(); err != nil {
			if s.onError != nil {
				s.onError(s.ext.Manifest.ID, "supervisor/restart", err)
			}
			continue // try again after backoff
		}

		// If the process stays alive for ResetAfter, reset the counter.
		if s.config.ResetAfter > 0 {
			go s.resetCounterAfter(s.config.ResetAfter)
		}
	}
}

// waitForExit returns a channel that closes when the extension process exits.
// Returns nil if no process is running.
func (s *extensionSupervisor) waitForExit() <-chan struct{} {
	ch := make(chan struct{})

	go func() {
		defer close(ch)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if !s.isRunning() {
					return
				}
			case <-s.done:
				return
			}
		}
	}()

	return ch
}

func (s *extensionSupervisor) isRunning() bool {
	if s.ext.Process != nil {
		return s.ext.Process.IsRunning()
	}
	if s.ext.GRPCProcess != nil {
		return s.ext.GRPCProcess.IsRunning()
	}
	return false
}

func (s *extensionSupervisor) restartExtension() error {
	if s.ext.Manifest.Protocol == GRPCProtocol {
		proc := NewGRPCProcessExtension(s.ext)
		proc.OnError = s.onError
		if err := proc.Start(); err != nil {
			return fmt.Errorf("restart gRPC extension: %w", err)
		}
		s.ext.GRPCProcess = proc
		s.ext.Hooks = proc.BuildHooks()
		s.ext.Tools = proc.BuildTools()
	} else {
		proc := NewProcessExtension(s.ext)
		proc.OnError = s.onError
		if err := proc.Start(); err != nil {
			return fmt.Errorf("restart JSON-RPC extension: %w", err)
		}
		s.ext.Process = proc
		s.ext.Hooks = proc.BuildHooks()
		s.ext.Tools = proc.BuildTools()
	}
	return nil
}

func (s *extensionSupervisor) resetCounterAfter(d time.Duration) {
	select {
	case <-time.After(d):
		s.mu.Lock()
		s.restarts = 0
		s.mu.Unlock()
	case <-s.done:
	}
}
