// Package agent provides SSH connection management for the ssh/sftp tools.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/sftp"
	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/secrets"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

// SSH connection limits and timeouts.
const (
	sshDefaultPort           = 22
	sshDefaultTimeout        = 30 * time.Second
	sshMaxTimeout            = 5 * time.Minute
	sshDefaultKeepAlive      = 60 * time.Second
	sshMaxConnections        = 10
	sshStaleConnectionTime   = 10 * time.Minute
	sshCleanupInterval       = 1 * time.Minute
	sshKeepaliveInterval     = 30 * time.Second
	sshDefaultKnownHostsFile = "~/.ssh/known_hosts"
)

// SSH errors.
var (
	ErrNoAuthMethod          = errors.New("no authentication method available")
	ErrNoAgent               = errors.New("ssh-agent not available (SSH_AUTH_SOCK not set)")
	ErrNoKeysInAgent         = errors.New("ssh-agent has no keys loaded")
	ErrHostNotConfigured     = errors.New("host not configured")
	ErrMaxConnectionsReached = errors.New("maximum SSH connections reached")
	ErrConnectionNotFound    = errors.New("connection not found")
	ErrHostKeyChanged        = errors.New("host key has changed - possible MITM attack")
	ErrInsecureNotAllowed    = errors.New("insecure host key check only allowed for localhost")
	ErrManagerShutdown       = errors.New("SSH manager is shutting down")
)

// SSHConnectionManager manages SSH host connections.
type SSHConnectionManager struct {
	mu          sync.RWMutex
	connections map[string]*sshConnection
	configs     map[string]config.SSHHost
	maxConns    int
	logger      *slog.Logger
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

// sshConnection represents an active SSH connection.
type sshConnection struct {
	id           string
	hostName     string
	client       *ssh.Client
	sftpClient   *sftp.Client
	sftpMu       sync.Mutex
	createdAt    time.Time
	lastActivity time.Time
	mu           sync.Mutex
}

// SSHConnInfo contains information about an SSH connection.
type SSHConnInfo struct {
	ID           string `json:"id"`
	Host         string `json:"host"`
	User         string `json:"user"`
	RemoteAddr   string `json:"remote_addr"`
	Connected    bool   `json:"connected"`
	CreatedAt    string `json:"created_at"`
	LastActivity string `json:"last_activity"`
}

// NewSSHConnectionManager creates a new SSH connection manager.
func NewSSHConnectionManager(logger *slog.Logger) *SSHConnectionManager {
	ctx, cancel := context.WithCancel(context.Background())
	mgr := &SSHConnectionManager{
		connections: make(map[string]*sshConnection),
		configs:     make(map[string]config.SSHHost),
		maxConns:    sshMaxConnections,
		logger:      logger,
		ctx:         ctx,
		cancel:      cancel,
	}
	mgr.wg.Add(2)
	go mgr.cleanupLoop()
	go mgr.keepaliveLoop()
	return mgr
}

// RegisterHost registers an SSH host configuration.
func (m *SSHConnectionManager) RegisterHost(name string, cfg config.SSHHost) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configs[name] = cfg
}

// HostNames returns a list of registered host names.
func (m *SSHConnectionManager) HostNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.configs))
	for name := range m.configs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Connect establishes an SSH connection to the named host.
func (m *SSHConnectionManager) Connect(ctx context.Context, hostName string) (string, error) {
	select {
	case <-m.ctx.Done():
		return "", ErrManagerShutdown
	default:
	}

	m.mu.Lock()
	cfg, ok := m.configs[hostName]
	if !ok {
		m.mu.Unlock()
		return "", fmt.Errorf("%w: %s", ErrHostNotConfigured, hostName)
	}

	// Check connection limit
	if len(m.connections) >= m.maxConns {
		m.mu.Unlock()
		return "", ErrMaxConnectionsReached
	}
	m.mu.Unlock()

	// Build SSH client config
	sshConfig, err := m.buildSSHConfig(cfg)
	if err != nil {
		return "", fmt.Errorf("failed to build SSH config: %w", err)
	}

	// Determine timeout
	timeout := sshDefaultTimeout
	if cfg.TimeoutSecs > 0 {
		timeout = time.Duration(cfg.TimeoutSecs) * time.Second
		if timeout > sshMaxTimeout {
			timeout = sshMaxTimeout
		}
	}

	// Connect with context timeout
	connCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var client *ssh.Client
	if cfg.ProxyJump != "" {
		// Connect through jump host
		client, err = m.dialWithProxyJump(connCtx, cfg)
	} else {
		// Direct connection
		client, err = m.dialDirect(connCtx, cfg, sshConfig)
	}
	if err != nil {
		return "", err
	}

	// Create connection record
	connID := uuid.NewString()[:8]
	conn := &sshConnection{
		id:           connID,
		hostName:     hostName,
		client:       client,
		createdAt:    time.Now(),
		lastActivity: time.Now(),
	}

	m.mu.Lock()
	m.connections[connID] = conn
	m.mu.Unlock()

	m.logger.Info("SSH connected", "id", connID, "host", hostName, "remote", client.RemoteAddr())
	return connID, nil
}

// dialDirect establishes a direct SSH connection.
func (m *SSHConnectionManager) dialDirect(ctx context.Context, cfg config.SSHHost, sshConfig *ssh.ClientConfig) (*ssh.Client, error) {
	port := cfg.Port
	if port == 0 {
		port = sshDefaultPort
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(port))

	// Dial with context
	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", addr, err)
	}

	// SSH handshake
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, sshConfig)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("SSH handshake failed: %w", err)
	}

	return ssh.NewClient(sshConn, chans, reqs), nil
}

// dialWithProxyJump establishes a connection through a jump host.
func (m *SSHConnectionManager) dialWithProxyJump(ctx context.Context, targetCfg config.SSHHost) (*ssh.Client, error) {
	// Get jump host config
	m.mu.RLock()
	jumpCfg, ok := m.configs[targetCfg.ProxyJump]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("proxy_jump host %q not configured", targetCfg.ProxyJump)
	}

	// Build configs for both
	jumpSSHConfig, err := m.buildSSHConfig(jumpCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to build jump host SSH config: %w", err)
	}

	targetSSHConfig, err := m.buildSSHConfig(targetCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to build target SSH config: %w", err)
	}

	// Connect to jump host (recursively handles nested jumps)
	var jumpClient *ssh.Client
	if jumpCfg.ProxyJump != "" {
		jumpClient, err = m.dialWithProxyJump(ctx, jumpCfg)
	} else {
		jumpClient, err = m.dialDirect(ctx, jumpCfg, jumpSSHConfig)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to connect to jump host %s: %w", targetCfg.ProxyJump, err)
	}

	// Dial target through jump host
	targetPort := targetCfg.Port
	if targetPort == 0 {
		targetPort = sshDefaultPort
	}
	targetAddr := net.JoinHostPort(targetCfg.Host, strconv.Itoa(targetPort))

	conn, err := jumpClient.Dial("tcp", targetAddr)
	if err != nil {
		jumpClient.Close()
		return nil, fmt.Errorf("failed to dial through jump host: %w", err)
	}

	// Create SSH connection over the proxied TCP connection
	ncc, chans, reqs, err := ssh.NewClientConn(conn, targetAddr, targetSSHConfig)
	if err != nil {
		conn.Close()
		jumpClient.Close()
		return nil, fmt.Errorf("SSH handshake through proxy failed: %w", err)
	}

	return ssh.NewClient(ncc, chans, reqs), nil
}

// buildSSHConfig creates an ssh.ClientConfig from SSHHost configuration.
func (m *SSHConnectionManager) buildSSHConfig(cfg config.SSHHost) (*ssh.ClientConfig, error) {
	// Build auth methods
	authMethods, err := m.resolveAuthMethods(cfg)
	if err != nil {
		return nil, err
	}
	if len(authMethods) == 0 {
		return nil, ErrNoAuthMethod
	}

	// Build host key callback
	hostKeyCallback, err := m.buildHostKeyCallback(cfg)
	if err != nil {
		return nil, err
	}

	return &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         sshDefaultTimeout,
	}, nil
}

// resolveAuthMethods builds SSH auth methods from configuration.
func (m *SSHConnectionManager) resolveAuthMethods(cfg config.SSHHost) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	// Priority 1: ssh-agent
	if cfg.UseAgent {
		signers, err := m.getSignersFromAgent()
		if err == nil && len(signers) > 0 {
			methods = append(methods, ssh.PublicKeys(signers...))
			m.logger.Debug("using ssh-agent for auth", "host", cfg.Host)
		} else if err != nil {
			m.logger.Debug("ssh-agent not available", "host", cfg.Host, "error", err)
		}
	}

	// Priority 2: Private key file or embedded key
	if cfg.PrivateKey != "" {
		signer, err := m.resolvePrivateKey(cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve private key: %w", err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
		m.logger.Debug("using private key for auth", "host", cfg.Host)
	}

	// Priority 3: Password auth
	if cfg.Password != "" {
		password := m.resolveSecret(cfg.Password)
		methods = append(methods, ssh.Password(password))
		m.logger.Debug("using password for auth", "host", cfg.Host)
	}

	return methods, nil
}

// getSignersFromAgent gets signers from the running ssh-agent.
func (m *SSHConnectionManager) getSignersFromAgent() ([]ssh.Signer, error) {
	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket == "" {
		return nil, ErrNoAgent
	}

	conn, err := net.Dial("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to ssh-agent: %w", err)
	}

	agentClient := agent.NewClient(conn)
	signers, err := agentClient.Signers()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("cannot get signers from agent: %w", err)
	}

	if len(signers) == 0 {
		conn.Close()
		return nil, ErrNoKeysInAgent
	}

	return signers, nil
}

// resolvePrivateKey resolves a private key from file or secrets store.
func (m *SSHConnectionManager) resolvePrivateKey(cfg config.SSHHost) (ssh.Signer, error) {
	var keyData []byte
	var err error

	if strings.HasPrefix(cfg.PrivateKey, "secret:") {
		// Embedded key from secrets store
		secretName := strings.TrimPrefix(cfg.PrivateKey, "secret:")
		store := secrets.DefaultStore()
		if store == nil || !store.Available() {
			return nil, errors.New("secrets store not available")
		}
		keyStr, err := store.Get(secretName)
		if err != nil {
			return nil, fmt.Errorf("failed to get private key from secrets: %w", err)
		}
		keyData = []byte(keyStr)
	} else {
		// Key file path
		keyPath := config.ExpandPath(cfg.PrivateKey)
		keyData, err = os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read private key file %s: %w", keyPath, err)
		}
	}

	// Resolve passphrase if needed
	var passphrase []byte
	if cfg.Passphrase != "" {
		passphrase = []byte(m.resolveSecret(cfg.Passphrase))
	}

	// Parse key
	var signer ssh.Signer
	if len(passphrase) > 0 {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(keyData, passphrase)
	} else {
		signer, err = ssh.ParsePrivateKey(keyData)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	return signer, nil
}

// resolveSecret resolves a secret value (handles secret: prefix).
func (m *SSHConnectionManager) resolveSecret(value string) string {
	if !strings.HasPrefix(value, "secret:") {
		return value
	}
	secretName := strings.TrimPrefix(value, "secret:")
	store := secrets.DefaultStore()
	if store == nil || !store.Available() {
		return value
	}
	resolved, err := store.Get(secretName)
	if err != nil {
		m.logger.Warn("failed to resolve secret", "name", secretName, "error", err)
		return value
	}
	return resolved
}

// buildHostKeyCallback creates a host key callback based on config.
func (m *SSHConnectionManager) buildHostKeyCallback(cfg config.SSHHost) (ssh.HostKeyCallback, error) {
	mode := cfg.HostKeyCheck
	if mode == "" {
		mode = "strict"
	}

	switch mode {
	case "none":
		// Only allow for localhost
		if !isLocalhost(cfg.Host) {
			return nil, ErrInsecureNotAllowed
		}
		m.logger.Warn("using insecure host key check", "host", cfg.Host)
		return ssh.InsecureIgnoreHostKey(), nil

	case "accept-new":
		return m.newAcceptNewCallback(cfg), nil

	default: // "strict"
		knownHostsPath := cfg.KnownHostsFile
		if knownHostsPath == "" {
			knownHostsPath = sshDefaultKnownHostsFile
		}
		knownHostsPath = config.ExpandPath(knownHostsPath)

		// Check if file exists
		if _, err := os.Stat(knownHostsPath); os.IsNotExist(err) {
			return nil, fmt.Errorf("known_hosts file not found: %s (use host_key_check: accept-new for first connection)", knownHostsPath)
		}

		callback, err := knownhosts.New(knownHostsPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load known_hosts: %w", err)
		}
		return callback, nil
	}
}

// newAcceptNewCallback creates a callback that accepts new keys but rejects changes.
func (m *SSHConnectionManager) newAcceptNewCallback(cfg config.SSHHost) ssh.HostKeyCallback {
	knownHostsPath := cfg.KnownHostsFile
	if knownHostsPath == "" {
		knownHostsPath = sshDefaultKnownHostsFile
	}
	knownHostsPath = config.ExpandPath(knownHostsPath)

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		// Try existing known_hosts first
		callback, err := knownhosts.New(knownHostsPath)
		if err == nil {
			if err := callback(hostname, remote, key); err == nil {
				return nil // Key already known and matches
			} else if keyErr := (*knownhosts.KeyError)(nil); errors.As(err, &keyErr) && len(keyErr.Want) > 0 {
				// Key changed - security violation
				return fmt.Errorf("%w: %s", ErrHostKeyChanged, hostname)
			}
		}

		// Key is new - log and accept
		m.logger.Info("accepting new host key", "host", hostname, "fingerprint", ssh.FingerprintSHA256(key))

		// Try to append to known_hosts (best effort)
		if err := appendKnownHost(knownHostsPath, hostname, key); err != nil {
			m.logger.Warn("failed to save new host key", "host", hostname, "error", err)
		}

		return nil
	}
}

// appendKnownHost appends a new host key to the known_hosts file.
func appendKnownHost(path string, hostname string, key ssh.PublicKey) error {
	// Ensure directory exists
	dir := config.ExpandPath("~/.ssh")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	// Format the known_hosts line
	line := knownhosts.Line([]string{hostname}, key)

	// Append to file
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.WriteString(line + "\n"); err != nil {
		return err
	}
	return nil
}

// isLocalhost checks if the host is localhost.
func isLocalhost(host string) bool {
	h := strings.ToLower(host)
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// GetConnection returns a connection by ID.
func (m *SSHConnectionManager) GetConnection(connID string) (*sshConnection, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	conn, ok := m.connections[connID]
	if !ok {
		return nil, ErrConnectionNotFound
	}
	return conn, nil
}

// GetSFTPClient returns or creates an SFTP client for a connection.
func (m *SSHConnectionManager) GetSFTPClient(connID string) (*sftp.Client, error) {
	conn, err := m.GetConnection(connID)
	if err != nil {
		return nil, err
	}

	conn.sftpMu.Lock()
	defer conn.sftpMu.Unlock()

	if conn.sftpClient != nil {
		return conn.sftpClient, nil
	}

	// Create SFTP client
	sftpClient, err := sftp.NewClient(conn.client)
	if err != nil {
		return nil, fmt.Errorf("failed to create SFTP client: %w", err)
	}
	conn.sftpClient = sftpClient

	return sftpClient, nil
}

// Disconnect closes an SSH connection.
func (m *SSHConnectionManager) Disconnect(connID string) error {
	m.mu.Lock()
	conn, ok := m.connections[connID]
	if !ok {
		m.mu.Unlock()
		return ErrConnectionNotFound
	}
	delete(m.connections, connID)
	m.mu.Unlock()

	return m.closeConnection(conn)
}

// closeConnection closes an SSH connection and its SFTP client.
func (m *SSHConnectionManager) closeConnection(conn *sshConnection) error {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	var errs []error

	// Close SFTP client first
	conn.sftpMu.Lock()
	if conn.sftpClient != nil {
		if err := conn.sftpClient.Close(); err != nil {
			errs = append(errs, fmt.Errorf("SFTP close: %w", err))
		}
		conn.sftpClient = nil
	}
	conn.sftpMu.Unlock()

	// Close SSH client
	if conn.client != nil {
		if err := conn.client.Close(); err != nil {
			errs = append(errs, fmt.Errorf("SSH close: %w", err))
		}
	}

	m.logger.Info("SSH disconnected", "id", conn.id, "host", conn.hostName)

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// ListConnections returns information about all active connections.
func (m *SSHConnectionManager) ListConnections() []SSHConnInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]SSHConnInfo, 0, len(m.connections))
	for _, conn := range m.connections {
		info := SSHConnInfo{
			ID:           conn.id,
			Host:         conn.hostName,
			CreatedAt:    conn.createdAt.Format(time.RFC3339),
			LastActivity: conn.lastActivity.Format(time.RFC3339),
			Connected:    true,
		}
		if conn.client != nil {
			info.RemoteAddr = conn.client.RemoteAddr().String()
			info.User = conn.client.User()
		}
		infos = append(infos, info)
	}

	// Sort by creation time
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].CreatedAt < infos[j].CreatedAt
	})

	return infos
}

// UpdateActivity marks a connection as recently used.
func (m *SSHConnectionManager) UpdateActivity(connID string) {
	m.mu.RLock()
	conn, ok := m.connections[connID]
	m.mu.RUnlock()
	if ok {
		conn.mu.Lock()
		conn.lastActivity = time.Now()
		conn.mu.Unlock()
	}
}

// cleanupLoop periodically removes stale connections.
func (m *SSHConnectionManager) cleanupLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(sshCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.cleanupStaleConnections()
		}
	}
}

// cleanupStaleConnections closes connections that have been idle too long.
func (m *SSHConnectionManager) cleanupStaleConnections() {
	now := time.Now()
	var toClose []*sshConnection

	m.mu.Lock()
	for id, conn := range m.connections {
		conn.mu.Lock()
		if now.Sub(conn.lastActivity) > sshStaleConnectionTime {
			toClose = append(toClose, conn)
			delete(m.connections, id)
		}
		conn.mu.Unlock()
	}
	m.mu.Unlock()

	for _, conn := range toClose {
		m.logger.Info("closing stale SSH connection", "id", conn.id, "host", conn.hostName)
		m.closeConnection(conn)
	}
}

// keepaliveLoop sends keepalive requests to maintain connections.
func (m *SSHConnectionManager) keepaliveLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(sshKeepaliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.sendKeepalives()
		}
	}
}

// sendKeepalives sends keepalive requests to all connections.
func (m *SSHConnectionManager) sendKeepalives() {
	m.mu.RLock()
	conns := make([]*sshConnection, 0, len(m.connections))
	for _, conn := range m.connections {
		conns = append(conns, conn)
	}
	m.mu.RUnlock()

	for _, conn := range conns {
		go func(c *sshConnection) {
			if c.client != nil {
				_, _, err := c.client.SendRequest("keepalive@openssh.com", true, nil)
				if err != nil {
					m.logger.Debug("SSH keepalive failed", "host", c.hostName, "error", err)
				}
			}
		}(conn)
	}
}

// Shutdown gracefully closes all connections and stops background loops.
func (m *SSHConnectionManager) Shutdown() error {
	m.cancel()
	m.wg.Wait()

	m.mu.Lock()
	conns := make([]*sshConnection, 0, len(m.connections))
	for _, conn := range m.connections {
		conns = append(conns, conn)
	}
	m.connections = make(map[string]*sshConnection)
	m.mu.Unlock()

	var errs []error
	for _, conn := range conns {
		if err := m.closeConnection(conn); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
