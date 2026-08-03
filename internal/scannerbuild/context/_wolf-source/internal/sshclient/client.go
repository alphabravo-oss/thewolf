package sshclient

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const DefaultTimeout = 30 * time.Second

type Config struct {
	Host       string
	Port       int
	Username   string
	PrivateKey string
	Password   string
	KnownHosts string
	Timeout    time.Duration
}

type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type Runner interface {
	Run(ctx context.Context, cfg Config, command string) (Result, error)
}

type Client struct{}

func (Client) Run(ctx context.Context, cfg Config, command string) (Result, error) {
	if cfg.Host == "" || cfg.Username == "" {
		return Result{}, fmt.Errorf("host and username are required")
	}
	if cfg.Port == 0 {
		cfg.Port = 22
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}
	authMethods := make([]ssh.AuthMethod, 0, 2)
	if cfg.PrivateKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(cfg.PrivateKey))
		if err != nil {
			return Result{}, fmt.Errorf("parse SSH private key: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}
	if cfg.Password != "" {
		authMethods = append(authMethods, ssh.Password(cfg.Password))
	}
	if len(authMethods) == 0 {
		return Result{}, fmt.Errorf("SSH credential is required")
	}
	hostKeyCallback, cleanup, err := hostKeyCallback(cfg.KnownHosts)
	if err != nil {
		return Result{}, err
	}
	defer cleanup()

	clientConfig := &ssh.ClientConfig{
		User:            cfg.Username,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         cfg.Timeout,
	}

	dialer := &net.Dialer{Timeout: cfg.Timeout}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return Result{}, fmt.Errorf("dial SSH: %w", err)
	}
	defer conn.Close()

	done := make(chan struct{})
	var sshConn ssh.Conn
	var chans <-chan ssh.NewChannel
	var reqs <-chan *ssh.Request
	var handshakeErr error
	go func() {
		sshConn, chans, reqs, handshakeErr = ssh.NewClientConn(conn, addr, clientConfig)
		close(done)
	}()
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case <-done:
		if handshakeErr != nil {
			return Result{}, fmt.Errorf("SSH handshake: %w", handshakeErr)
		}
	}

	client := ssh.NewClient(sshConn, chans, reqs)
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return Result{}, fmt.Errorf("new SSH session: %w", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	runDone := make(chan error, 1)
	go func() { runDone <- session.Run(command) }()
	select {
	case <-ctx.Done():
		_ = session.Close()
		return Result{Stdout: stdout.String(), Stderr: stderr.String()}, ctx.Err()
	case err := <-runDone:
		res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
		if err != nil {
			if exitErr, ok := err.(*ssh.ExitError); ok {
				res.ExitCode = exitErr.ExitStatus()
				return res, fmt.Errorf("remote command exited %d: %s", res.ExitCode, stderr.String())
			}
			return res, fmt.Errorf("run remote command: %w", err)
		}
		return res, nil
	}
}

func hostKeyCallback(knownHosts string) (ssh.HostKeyCallback, func(), error) {
	if knownHosts == "" {
		if os.Getenv("WOLF_SSH_INSECURE_SKIP_HOST_KEY") == "true" {
			return ssh.InsecureIgnoreHostKey(), func() {}, nil //nolint:gosec // explicit dev/test escape hatch
		}
		return nil, func() {}, fmt.Errorf("known_hosts is required for SSH host key verification")
	}
	tmp, err := os.CreateTemp("", "wolf-known-hosts-*")
	if err != nil {
		return nil, func() {}, fmt.Errorf("create known_hosts temp file: %w", err)
	}
	cleanup := func() {
		_ = os.Remove(tmp.Name())
	}
	if _, err := tmp.WriteString(knownHosts); err != nil {
		_ = tmp.Close()
		cleanup()
		return nil, func() {}, fmt.Errorf("write known_hosts temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("close known_hosts temp file: %w", err)
	}
	cb, err := knownhosts.New(tmp.Name())
	if err != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("parse known_hosts: %w", err)
	}
	return cb, cleanup, nil
}

func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	out := "'"
	for _, r := range s {
		if r == '\'' {
			out += "'\\''"
			continue
		}
		out += string(r)
	}
	out += "'"
	return out
}
