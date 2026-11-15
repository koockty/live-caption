package cloud

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLiveTranslateOverridesUnsupportedDefaultVoice(t *testing.T) {
	checked := make(chan bool, 1)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		c.WriteJSON(map[string]any{"type": "session.created", "session": map[string]any{"voice": "Chelsie"}})
		var update struct {
			Session struct {
				Voice      string   `json:"voice"`
				Modalities []string `json:"output_modalities"`
				Clone      bool     `json:"enable_voice_clone"`
				Audio      struct {
					Output struct {
						Voice string `json:"voice"`
					} `json:"output"`
				} `json:"audio"`
			} `json:"session"`
		}
		if err := c.ReadJSON(&update); err != nil {
			checked <- false
			return
		}
		valid := update.Session.Voice == "Tina" && update.Session.Audio.Output.Voice == "Tina" && !update.Session.Clone && len(update.Session.Modalities) == 1 && update.Session.Modalities[0] == "text"
		checked <- valid
		if !valid {
			c.WriteJSON(map[string]any{"type": "error", "error": map[string]string{"code": "COMMON_ERROR", "message": "Voice Chelsie is not supported"}})
			return
		}
		c.WriteJSON(map[string]string{"type": "session.updated"})
		c.ReadMessage()
	}))
	defer upstream.Close()
	pool := x509.NewCertPool()
	pool.AddCert(upstream.Certificate())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := OpenASR(ctx, ASRConfig{URL: "wss" + strings.TrimPrefix(upstream.URL, "https"), APIKey: "test-only", LiveTranslate: true, Dialer: &websocket.Dialer{TLSClientConfig: &tls.Config{RootCAs: pool}}})
	if err != nil {
		t.Fatal(err)
	}
	stream.Close()
	if !<-checked {
		t.Fatal("unsupported default voice was not overridden")
	}
}
