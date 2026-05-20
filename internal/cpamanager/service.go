package cpamanager

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cpa-control-center/internal/cpamanager/collector"
	"cpa-control-center/internal/cpamanager/config"
	"cpa-control-center/internal/cpamanager/httpapi"
	"cpa-control-center/internal/cpamanager/store"
)

const (
	DefaultHTTPHost = "127.0.0.1"
	DefaultHTTPPort = 18317

	defaultCollectorMode = "auto"
	defaultQueue         = "usage"
	defaultPopSide       = "right"
	defaultBatchSize     = 100
	defaultPollInterval  = 500 * time.Millisecond
	defaultQueryLimit    = 50000
)

type StartConfig struct {
	CPAUpstreamURL string
	ManagementKey  string
	HTTPHost       string
	HTTPPort       int
}

type RuntimeInfo struct {
	HTTPAddr      string
	BaseURL       string
	ManagementURL string
	HealthURL     string
	DBPath        string
}

type Service struct {
	dataDir string

	mu          sync.Mutex
	server      *http.Server
	store       *store.Store
	collector   *collector.Manager
	cancel      context.CancelFunc
	runtime     RuntimeInfo
	startConfig StartConfig
}

func New(dataDir string) *Service {
	return &Service{
		dataDir: strings.TrimSpace(dataDir),
	}
}

func (s *Service) Start(ctx context.Context, input StartConfig) (RuntimeInfo, error) {
	startConfig := normalizeStartConfig(input)

	s.mu.Lock()
	if s.server != nil && sameStartConfig(s.startConfig, startConfig) {
		runtime := s.runtime
		s.mu.Unlock()
		return runtime, nil
	}
	alreadyRunning := s.server != nil
	s.mu.Unlock()

	if alreadyRunning {
		if err := s.Stop(context.Background()); err != nil {
			return RuntimeInfo{}, err
		}
	}

	dataDir := s.dataDir
	if dataDir == "" {
		dataDir = filepath.Join(".", "cpa-manager")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return RuntimeInfo{}, fmt.Errorf("创建 CPA-Manager 数据目录失败: %w", err)
	}

	listener, httpAddr, baseURL, err := listenOnAvailablePort(startConfig.HTTPHost, startConfig.HTTPPort)
	if err != nil {
		return RuntimeInfo{}, err
	}

	cfg := config.Config{
		HTTPAddr:       httpAddr,
		DBPath:         filepath.Join(dataDir, "usage.sqlite"),
		CPAUpstreamURL: startConfig.CPAUpstreamURL,
		ManagementKey:  startConfig.ManagementKey,
		CollectorMode:  defaultCollectorMode,
		Queue:          defaultQueue,
		PopSide:        defaultPopSide,
		BatchSize:      defaultBatchSize,
		PollInterval:   defaultPollInterval,
		QueryLimit:     defaultQueryLimit,
		CORSOrigins:    []string{"*"},
	}

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		_ = listener.Close()
		return RuntimeInfo{}, fmt.Errorf("打开 CPA-Manager SQLite 失败: %w", err)
	}

	manager := collector.NewManager(cfg, db)
	runCtx, cancel := context.WithCancel(ctx)
	if cfg.CPAUpstreamURL != "" && cfg.ManagementKey != "" {
		manager.Start(runCtx, collector.RuntimeConfig{
			CPAUpstreamURL: cfg.CPAUpstreamURL,
			ManagementKey:  cfg.ManagementKey,
			CollectorMode:  cfg.CollectorMode,
			Queue:          cfg.Queue,
			PopSide:        cfg.PopSide,
			BatchSize:      cfg.BatchSize,
			PollInterval:   cfg.PollInterval,
			TLSSkipVerify:  cfg.TLSSkipVerify,
		})
	}

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.New(cfg, db, manager).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	runtime := RuntimeInfo{
		HTTPAddr:      httpAddr,
		BaseURL:       baseURL,
		ManagementURL: baseURL + "/management.html",
		HealthURL:     baseURL + "/health",
		DBPath:        cfg.DBPath,
	}

	s.mu.Lock()
	s.server = server
	s.store = db
	s.collector = manager
	s.cancel = cancel
	s.runtime = runtime
	s.startConfig = startConfig
	s.mu.Unlock()

	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			s.mu.Lock()
			if s.server == server {
				s.server = nil
				s.store = nil
				s.collector = nil
				s.cancel = nil
				s.runtime = RuntimeInfo{}
				s.startConfig = StartConfig{}
			}
			s.mu.Unlock()
		}
	}()

	return runtime, nil
}

func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	server := s.server
	db := s.store
	manager := s.collector
	cancel := s.cancel
	s.server = nil
	s.store = nil
	s.collector = nil
	s.cancel = nil
	s.runtime = RuntimeInfo{}
	s.startConfig = StartConfig{}
	s.mu.Unlock()

	if manager != nil {
		manager.Stop()
	}

	var firstErr error
	if server != nil {
		if ctx == nil {
			ctx = context.Background()
		}
		shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 5*time.Second)
		if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			firstErr = err
		}
		shutdownCancel()
	}
	if cancel != nil {
		cancel()
	}
	if db != nil {
		if err := db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Service) Runtime() (RuntimeInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.server == nil {
		return RuntimeInfo{}, false
	}
	return s.runtime, true
}

func normalizeStartConfig(input StartConfig) StartConfig {
	output := StartConfig{
		CPAUpstreamURL: strings.TrimRight(strings.TrimSpace(input.CPAUpstreamURL), "/"),
		ManagementKey:  strings.TrimSpace(input.ManagementKey),
		HTTPHost:       strings.TrimSpace(input.HTTPHost),
		HTTPPort:       input.HTTPPort,
	}
	if output.HTTPHost == "" {
		output.HTTPHost = DefaultHTTPHost
	}
	if output.HTTPPort <= 0 {
		output.HTTPPort = DefaultHTTPPort
	}
	return output
}

func sameStartConfig(left StartConfig, right StartConfig) bool {
	return left.CPAUpstreamURL == right.CPAUpstreamURL &&
		left.ManagementKey == right.ManagementKey &&
		left.HTTPHost == right.HTTPHost &&
		left.HTTPPort == right.HTTPPort
}

func listenOnAvailablePort(host string, firstPort int) (net.Listener, string, string, error) {
	var lastErr error
	for port := firstPort; port < firstPort+10; port++ {
		addr := fmt.Sprintf("%s:%d", host, port)
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			lastErr = err
			continue
		}
		httpAddr := listener.Addr().String()
		baseURL := fmt.Sprintf("http://%s:%d", host, port)
		return listener, httpAddr, baseURL, nil
	}
	return nil, "", "", fmt.Errorf("启动 CPA-Manager 监听失败: %w", lastErr)
}
