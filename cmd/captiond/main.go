package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"livecaptions/internal/caption"
	"livecaptions/internal/cloud"
	"livecaptions/internal/config"
	"livecaptions/internal/server"
)

func main() {
	envFile := flag.String("env", ".env", "本地环境配置文件")
	flag.Parse()
	if err := run(*envFile); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}

func run(envFile string) error {
	if err := config.LoadEnvFile(envFile); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("无法监听 %s：%w", cfg.Listen, err)
	}
	defer listener.Close()
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts := caption.DefaultOptions()
	opts.DirectTranslation = cfg.Pipeline == "livetranslate"
	manager := caption.NewManager(root, func(ctx context.Context) (caption.ASRStream, error) {
		return cloud.OpenASR(ctx, cloud.ASRConfig{URL: cfg.ASRURL, APIKey: cfg.APIKey, WorkspaceID: cfg.WorkspaceID, LiveTranslate: opts.DirectTranslation})
	}, &cloud.Translator{URL: cfg.TranslateURL, APIKey: cfg.APIKey}, opts)
	app := server.New(manager, cfg.APIKey != "", cfg.Pipeline)
	httpServer := &http.Server{Handler: app, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	serverErr := make(chan error, 1)
	go func() { serverErr <- httpServer.Serve(listener) }()
	slog.Info("Live Captions 已启动", "url", "http://"+listener.Addr().String(), "cloud_key_configured", cfg.APIKey != "")
	if cfg.APIKey == "" {
		slog.Info("尚未配置 API Key；可查看界面。填写 .env 后重启以启用识别和翻译。")
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	select {
	case err := <-serverErr:
		if err != http.ErrServerClosed {
			return fmt.Errorf("本地 HTTP 服务已停止")
		}
	case <-signals:
	}
	finished := manager.Stop()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return err
	}
	// HTTP Shutdown does not wait for hijacked WebSocket connections. Keep the
	// root context alive while ASR finalizes its tail and translation drains.
	select {
	case <-finished:
		return nil
	case <-shutdownCtx.Done():
		return fmt.Errorf("等待语音会话结束超时")
	}
}
