package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"livecaptions/internal/caption"
	"livecaptions/internal/web"
)

type App struct {
	manager    *caption.Manager
	configured bool
	token      string
	handler    http.Handler
}

func New(manager *caption.Manager, configured bool, pipeline ...string) *App {
	a := &App{manager: manager, configured: configured, token: caption.ID() + caption.ID()}
	mux := http.NewServeMux()
	assets, _ := fs.Sub(web.Files, "static")
	files := http.FileServer(http.FS(assets))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "lc_local", Value: a.token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		files.ServeHTTP(w, r)
	})
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", files))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "configured": configured, "version": "0.1.0-prototype"})
	})
	mux.HandleFunc("GET /v1/config", a.authorize(func(w http.ResponseWriter, _ *http.Request) {
		if len(pipeline) > 0 && pipeline[0] == "livetranslate" {
			writeJSON(w, 200, map[string]any{"configured": a.configured, "asr_model": "qwen3.8-livetranslate-flash-realtime", "translation_model": "qwen3.8-livetranslate-flash-realtime", "source_language": "auto", "target_language": "zh-CN", "sample_rate": caption.SampleRate})
			return
		}
		writeJSON(w, 200, map[string]any{"configured": a.configured, "asr_model": "qwen3-asr-flash-realtime", "translation_model": "qwen-mt-flash", "source_language": "ja", "target_language": "zh-CN", "sample_rate": caption.SampleRate})
	}))
	mux.HandleFunc("POST /v1/sessions", a.authorize(a.createSession))
	mux.HandleFunc("GET /v1/sessions/current", a.authorize(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"snapshot": a.manager.CurrentSnapshot()})
	}))
	mux.HandleFunc("GET /v1/sessions/{id}", a.authorize(func(w http.ResponseWriter, r *http.Request) {
		if s := a.session(w, r); s != nil {
			writeJSON(w, 200, s.Snapshot())
		}
	}))
	mux.HandleFunc("POST /v1/sessions/{id}/stop", a.authorize(func(w http.ResponseWriter, r *http.Request) {
		if s := a.session(w, r); s != nil {
			s.EndInput()
			writeJSON(w, 200, s.Snapshot())
		}
	}))
	mux.HandleFunc("GET /v1/sessions/{id}/audio", a.authorize(a.audio))
	mux.HandleFunc("GET /v1/sessions/{id}/events", a.authorize(a.events))
	a.handler = a.guard(mux)
	return a
}

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) { a.handler.ServeHTTP(w, r) }

func sameOrigin(r *http.Request) bool {
	origin, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && origin.Scheme == "http" && origin.Host == r.Host && origin.Path == "" && origin.RawQuery == "" && origin.User == nil
}

func (a *App) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			writeError(w, 403, "仅允许通过本机回环地址访问")
			return
		}
		if (r.Header.Get("Origin") != "" || r.Method != http.MethodGet) && !sameOrigin(r) {
			writeError(w, 403, "拒绝跨来源请求，请直接在 Chrome 打开本地采集页")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "microphone=(), camera=(), display-capture=(self)")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self' ws://"+r.Host+"; worker-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
		next.ServeHTTP(w, r)
	})
}

func (a *App) authorize(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("lc_local")
		if err != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(a.token)) != 1 {
			writeError(w, 401, "请先打开本地采集网页建立会话")
			return
		}
		if websocket.IsWebSocketUpgrade(r) && !sameOrigin(r) {
			writeError(w, 403, "WebSocket 来源无效")
			return
		}
		next.ServeHTTP(w, r)
	}
}

func (a *App) createSession(w http.ResponseWriter, r *http.Request) {
	if !a.configured {
		writeError(w, 503, "尚未配置 DASHSCOPE_API_KEY。请在 .env 中填写，或设置环境变量后重新启动 Go 服务。")
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, 415, "请求需要 application/json")
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	var request struct{}
	if err := decoder.Decode(&request); err != nil {
		writeError(w, 400, "请求正文应为 JSON 对象 {}")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, 400, "请求包含多余内容")
		return
	}
	s, err := a.manager.Create()
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	writeJSON(w, 201, s.Snapshot())
}

func (a *App) session(w http.ResponseWriter, r *http.Request) *caption.Session {
	s := a.manager.Get(r.PathValue("id"))
	if s == nil {
		writeError(w, 404, "会话不存在或已被新会话替换")
	}
	return s
}

func (a *App) audio(w http.ResponseWriter, r *http.Request) {
	s := a.session(w, r)
	if s == nil {
		return
	}
	upgrader := websocket.Upgrader{CheckOrigin: sameOrigin, ReadBufferSize: 4096, WriteBufferSize: 1024}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	if err := s.Start(); err != nil {
		conn.SetWriteDeadline(time.Now().Add(time.Second))
		conn.WriteJSON(map[string]string{"type": "error", "message": err.Error()})
		return
	}
	defer s.EndInput()
	conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if err := conn.WriteJSON(map[string]string{"type": "ready"}); err != nil {
		return
	}
	conn.SetReadLimit(caption.FrameHeaderBytes + caption.FrameBytes)
	closed := make(chan struct{})
	defer close(closed)
	go func() {
		select {
		case <-s.Done():
			conn.Close()
		case <-closed:
		}
	}()
	for {
		conn.SetReadDeadline(time.Now().Add(12 * time.Second))
		kind, frame, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if kind == websocket.TextMessage {
			var control struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(frame, &control) == nil && control.Type == "audio.end" {
				return
			}
			s.Fail("未知音频控制事件")
			return
		}
		if kind != websocket.BinaryMessage {
			continue
		}
		if err := s.PushFrame(frame); err != nil {
			if !errors.Is(err, caption.ErrClosed) {
				s.Fail(err.Error())
			}
			return
		}
	}
}

func (a *App) events(w http.ResponseWriter, r *http.Request) {
	s := a.session(w, r)
	if s == nil {
		return
	}
	upgrader := websocket.Upgrader{CheckOrigin: sameOrigin}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	snapshot, events, unsubscribe := s.Subscribe()
	defer unsubscribe()
	conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if err := conn.WriteJSON(map[string]any{"type": "snapshot", "snapshot": snapshot}); err != nil {
		return
	}
	conn.SetReadLimit(1024)
	conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(45 * time.Second)) })
	peerDone := make(chan struct{})
	go func() {
		defer close(peerDone)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return
			}
			conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
			if err := conn.WriteJSON(event); err != nil {
				return
			}
		case <-ticker.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(3*time.Second)); err != nil {
				return
			}
		case <-peerDone:
			return
		case <-r.Context().Done():
			return
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
