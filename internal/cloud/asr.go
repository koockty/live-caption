package cloud

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"livecaptions/internal/caption"
)

type ASRConfig struct {
	LiveTranslate bool
	URL           string
	APIKey        string
	WorkspaceID   string
	Dialer        *websocket.Dialer
}

type asrStream struct {
	apiKey    string
	live      bool
	sources   map[string]string
	links     map[string]string
	conn      *websocket.Conn
	mu        sync.Mutex
	closeOnce sync.Once
	done      chan struct{}
}

func OpenASR(ctx context.Context, cfg ASRConfig) (caption.ASRStream, error) {
	dialer := cfg.Dialer
	if dialer == nil {
		dialer = &websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: 10 * time.Second}
	}
	headers := http.Header{"Authorization": {"Bearer " + cfg.APIKey}}
	if cfg.WorkspaceID != "" {
		headers.Set("X-DashScope-WorkSpace", cfg.WorkspaceID)
	}
	conn, response, err := dialer.DialContext(ctx, cfg.URL, headers)
	if err != nil {
		if response != nil {
			response.Body.Close()
			return nil, fmt.Errorf("语音服务连接失败（HTTP %d），请检查 Key、地域和模型权限", response.StatusCode)
		}
		return nil, fmt.Errorf("无法连接语音服务，请检查网络或代理设置")
	}
	s := &asrStream{conn: conn, done: make(chan struct{}), live: cfg.LiveTranslate, apiKey: cfg.APIKey, sources: make(map[string]string), links: make(map[string]string)}
	conn.SetReadLimit(1 << 20)
	conn.SetReadDeadline(time.Now().Add(12 * time.Second))
	go func() {
		select {
		case <-ctx.Done():
			s.Close()
		case <-s.done:
		}
	}()
	update := map[string]any{
		"type": "session.update", "event_id": caption.ID(),
		"session": map[string]any{
			"input_audio_format": "pcm", "sample_rate": caption.SampleRate,
			"input_audio_transcription": map[string]any{"language": "ja"},
			"turn_detection":            map[string]any{"type": "server_vad", "threshold": 0.2, "silence_duration_ms": 600},
		},
	}
	if cfg.LiveTranslate {
		var created caption.ASREvent
		if err := conn.ReadJSON(&created); err != nil || created.Type != "session.created" {
			s.Close()
			return nil, fmt.Errorf("实时翻译会话初始化失败")
		}
		update["session"] = map[string]any{"voice": "Tina", "enable_voice_clone": false, "output_modalities": []string{"text"}, "audio": map[string]any{"output": map[string]any{"voice": "Tina"}, "input": map[string]any{"format": map[string]any{"type": "pcm", "sample_rate": 16000}, "turn_detection": map[string]any{"type": "server_vad", "threshold": 0.2, "silence_duration_ms": 600}}}, "translation": map[string]any{"language": "zh"}}
	}
	if err := s.write(update); err != nil {
		s.Close()
		return nil, fmt.Errorf("语音服务初始化失败")
	}
	for {
		event, err := s.Receive()
		if err != nil {
			s.Close()
			return nil, err
		}
		if event.Type == "session.updated" {
			conn.SetReadDeadline(time.Time{})
			return s, nil
		}
	}
}

func (s *asrStream) write(value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return s.conn.WriteJSON(value)
}

func (s *asrStream) SendPCM(pcm []byte) error {
	return s.write(map[string]any{"type": "input_audio_buffer.append", "event_id": caption.ID(), "audio": base64.StdEncoding.EncodeToString(pcm)})
}

func (s *asrStream) Receive() (caption.ASREvent, error) {
	var raw struct {
		caption.ASREvent
		PreviousItemID string `json:"previous_item_id"`
		Item           struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"item"`
		Response struct {
			Status string `json:"status"`
			Usage  struct {
				Input  int `json:"input_tokens"`
				Output int `json:"output_tokens"`
			} `json:"usage"`
		} `json:"response"`
	}
	if err := s.conn.ReadJSON(&raw); err != nil {
		return raw.ASREvent, fmt.Errorf("语音服务连接中断，请重新开始")
	}
	event := raw.ASREvent
	if s.live {
		switch event.Type {
		case "conversation.item.created":
			if raw.Item.Role == "assistant" {
				s.links[raw.Item.ID] = raw.PreviousItemID
			}
		case "conversation.item.input_audio_transcription.delta":
			s.sources[event.ItemID] += event.Delta
			event.Type = "conversation.item.input_audio_transcription.text"
			event.Text = s.sources[event.ItemID]
		case "conversation.item.input_audio_transcription.completed":
			delete(s.sources, event.ItemID)
		case "response.text.delta":
			event.Type = "translation.delta"
			event.SourceItemID = s.links[event.ItemID]
		case "response.text.done":
			event.Type = "translation.done"
			event.SourceItemID = s.links[event.ItemID]
			delete(s.links, event.ItemID)
		case "response.done":
			if raw.Response.Status != "completed" {
				return event, fmt.Errorf("实时翻译响应未完整完成")
			}
			event.Type = "translation.usage"
			event.InputTokens = raw.Response.Usage.Input
			event.OutputTokens = raw.Response.Usage.Output
		}
	}
	if event.Type == "error" || event.Type == "conversation.item.input_audio_transcription.failed" {
		return event, fmt.Errorf("实时语音服务错误 [%s]: %s", event.Error.Code, strings.ReplaceAll(event.Error.Message, s.apiKey, "[REDACTED]"))
	}
	return event, nil
}

func (s *asrStream) Finish() error {
	return s.write(map[string]any{"type": "session.finish", "event_id": caption.ID()})
}

func (s *asrStream) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.done)
		err = s.conn.Close()
	})
	return err
}
