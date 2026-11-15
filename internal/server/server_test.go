package server_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"livecaptions/internal/caption"
	"livecaptions/internal/cloud"
	"livecaptions/internal/server"
)

const fixtureKey = "test-only-not-a-real-api-key"

type cloudFixture struct {
	t                 *testing.T
	server            *httptest.Server
	mu                sync.Mutex
	pcm               []byte
	translations      []string
	finish            atomic.Bool
	translationStatus int
	asrStatus         int
	signalDetected    atomic.Bool
	interactive       bool
}

func newCloudFixture(t *testing.T) *cloudFixture {
	f := &cloudFixture{t: t}
	f.server = httptest.NewTLSServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *cloudFixture) handle(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+fixtureKey {
		f.t.Error("cloud Authorization missing")
		http.Error(w, "unauthorized", 401)
		return
	}
	if r.URL.Path == "/asr" {
		f.asr(w, r)
		return
	}
	if r.URL.Path != "/translate" {
		http.NotFound(w, r)
		return
	}
	var request struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Options struct {
			Source string `json:"source_lang"`
			Target string `json:"target_lang"`
		} `json:"translation_options"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		f.t.Error(err)
		return
	}
	if request.Model != "qwen-mt-flash" || request.Stream || request.Options.Source != "Japanese" || request.Options.Target != "Chinese" || len(request.Messages) != 1 || request.Messages[0].Role != "user" {
		f.t.Error("invalid Qwen MT request")
		http.Error(w, "bad schema", 400)
		return
	}
	f.mu.Lock()
	f.translations = append(f.translations, request.Messages[0].Content)
	f.mu.Unlock()
	if f.translationStatus != 0 {
		http.Error(w, "fixture limit", f.translationStatus)
		return
	}
	text := "今天天气很好。明天也会晴朗。"
	if f.interactive {
		text = "这是音频采集测试。浏览器到 Go 的链路已连通。"
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": text}}}, "usage": map[string]int{"prompt_tokens": 30, "completion_tokens": 20}})
}

func (f *cloudFixture) asr(w http.ResponseWriter, r *http.Request) {
	if f.asrStatus != 0 {
		http.Error(w, "fixture rejected", f.asrStatus)
		return
	}
	if r.URL.Query().Get("model") != "qwen3-asr-flash-realtime" {
		f.t.Error("missing ASR model")
	}
	conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
	if err != nil {
		f.t.Error(err)
		return
	}
	defer conn.Close()
	conn.WriteJSON(map[string]any{"type": "session.created"})
	var init struct {
		Type    string `json:"type"`
		Session struct {
			Format        string `json:"input_audio_format"`
			Rate          int    `json:"sample_rate"`
			Transcription struct {
				Language string `json:"language"`
			} `json:"input_audio_transcription"`
			VAD struct {
				Type    string `json:"type"`
				Silence int    `json:"silence_duration_ms"`
			} `json:"turn_detection"`
		} `json:"session"`
	}
	if err := conn.ReadJSON(&init); err != nil {
		f.t.Error(err)
		return
	}
	if init.Type != "session.update" || init.Session.Format != "pcm" || init.Session.Rate != 16000 || init.Session.Transcription.Language != "ja" || init.Session.VAD.Type != "server_vad" || init.Session.VAD.Silence != 600 {
		f.t.Error("invalid Qwen ASR session configuration")
		return
	}
	conn.WriteJSON(map[string]string{"type": "session.updated"})
	frames := 0
	for {
		var event struct {
			Type  string `json:"type"`
			Audio string `json:"audio"`
		}
		if err := conn.ReadJSON(&event); err != nil {
			return
		}
		switch event.Type {
		case "input_audio_buffer.append":
			data, err := base64.StdEncoding.DecodeString(event.Audio)
			if err != nil || len(data)%2 != 0 {
				f.t.Error("invalid cloud audio")
				return
			}
			f.mu.Lock()
			f.pcm = append(f.pcm, data...)
			f.mu.Unlock()
			frames++
			for i := 0; i+1 < len(data); i += 2 {
				if value := int16(binary.LittleEndian.Uint16(data[i:])); value > 100 || value < -100 {
					f.signalDetected.Store(true)
					break
				}
			}
			if frames == 1 {
				conn.WriteJSON(map[string]string{"type": "conversation.item.input_audio_transcription.text", "item_id": "j1", "text": "今日は", "stash": "いい"})
				conn.WriteJSON(map[string]string{"type": "conversation.item.input_audio_transcription.text", "item_id": "j1", "text": "今日は", "stash": "いい天気です。"})
			}
			if f.interactive && frames == 20 {
				conn.WriteJSON(map[string]string{"type": "conversation.item.input_audio_transcription.completed", "item_id": "browser-check", "transcript": "これは音声キャプチャのテストです。ブラウザからGoへの接続を確認しました。"})
			}
		case "session.finish":
			f.finish.Store(true)
			if !f.interactive {
				for _, event := range []map[string]string{
					{"type": "conversation.item.input_audio_transcription.completed", "item_id": "j1", "transcript": "今日はいい天気です。"},
					{"type": "conversation.item.input_audio_transcription.completed", "item_id": "j1", "transcript": "今日はいい天気です。"},
					{"type": "conversation.item.input_audio_transcription.completed", "item_id": "j2", "transcript": "明日も晴れるでしょう。"},
				} {
					conn.WriteJSON(event)
				}
			}
			conn.WriteJSON(map[string]string{"type": "session.finished"})
			return
		}
	}
}

func fixtureManager(t *testing.T, f *cloudFixture) *caption.Manager {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pool := x509.NewCertPool()
	pool.AddCert(f.server.Certificate())
	dialer := &websocket.Dialer{TLSClientConfig: &tls.Config{RootCAs: pool}, HandshakeTimeout: time.Second}
	return caption.NewManager(ctx, func(ctx context.Context) (caption.ASRStream, error) {
		return cloud.OpenASR(ctx, cloud.ASRConfig{URL: "wss" + strings.TrimPrefix(f.server.URL, "https") + "/asr?model=qwen3-asr-flash-realtime", APIKey: fixtureKey, Dialer: dialer})
	}, &cloud.Translator{URL: f.server.URL + "/translate", APIKey: fixtureKey, Client: f.server.Client()}, caption.Options{TranslationInterval: time.Millisecond, BatchWindow: 20 * time.Millisecond, TranslationTimeout: time.Second, FinishTimeout: time.Second})
}

type localClient struct {
	t    *testing.T
	base string
	http *http.Client
}

func bootstrap(t *testing.T, base string) *localClient {
	jar, _ := cookiejar.New(nil)
	c := &localClient{t, base, &http.Client{Jar: jar, Timeout: 3 * time.Second}}
	response, err := c.http.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 || !strings.Contains(string(data), "日语实时字幕") || strings.Contains(string(data), fixtureKey) {
		t.Fatal("bootstrap page invalid or leaks credentials")
	}
	return c
}

func (c *localClient) request(method, path string) (*http.Response, []byte) {
	c.t.Helper()
	request, _ := http.NewRequest(method, c.base+path, strings.NewReader("{}"))
	request.Header.Set("Origin", c.base)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		c.t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	if strings.Contains(string(data), fixtureKey) {
		c.t.Fatal("response leaked API Key")
	}
	return response, data
}

func (c *localClient) socket(path string) *websocket.Conn {
	c.t.Helper()
	u, _ := url.Parse(c.base)
	header := http.Header{"Origin": {c.base}}
	for _, cookie := range c.http.Jar.Cookies(u) {
		header.Add("Cookie", cookie.String())
	}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(c.base, "http")+path, header)
	if err != nil {
		c.t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	c.t.Cleanup(func() { conn.Close() })
	return conn
}

func TestHTTPAudioToASRTranslationAndTailDrain(t *testing.T) {
	for _, translationStatus := range []int{0, http.StatusTooManyRequests} {
		t.Run(fmt.Sprintf("translation_status_%d", translationStatus), func(t *testing.T) {
			cloudStub := newCloudFixture(t)
			cloudStub.translationStatus = translationStatus
			manager := fixtureManager(t, cloudStub)
			app := httptest.NewServer(server.New(manager, true))
			defer app.Close()
			client := bootstrap(t, app.URL)
			response, body := client.request("POST", "/v1/sessions")
			if response.StatusCode != 201 {
				t.Fatal(string(body))
			}
			var created caption.Snapshot
			json.Unmarshal(body, &created)
			currentResponse, currentBody := client.request("GET", "/v1/sessions/current")
			var current struct {
				Snapshot *caption.Snapshot `json:"snapshot"`
			}
			if err := json.Unmarshal(currentBody, &current); err != nil || currentResponse.StatusCode != 200 || current.Snapshot == nil || current.Snapshot.SessionID != created.SessionID {
				t.Fatal("desktop current session discovery failed")
			}
			events := client.socket("/v1/sessions/" + created.SessionID + "/events")
			audio := client.socket("/v1/sessions/" + created.SessionID + "/audio")
			var ready map[string]string
			if err := audio.ReadJSON(&ready); err != nil || ready["type"] != "ready" {
				t.Fatalf("ASR handshake did not complete: %v", err)
			}
			for index := uint32(0); index < 2; index++ {
				frame := make([]byte, 3212)
				binary.LittleEndian.PutUint32(frame, index)
				binary.LittleEndian.PutUint64(frame[4:], uint64(index)*1600)
				binary.LittleEndian.PutUint16(frame[12:], 1234)
				if err := audio.WriteMessage(websocket.BinaryMessage, frame); err != nil {
					t.Fatal(err)
				}
			}
			partials := 0
			for partials < 2 {
				var event struct {
					Type       string             `json:"type"`
					Transcript caption.Transcript `json:"transcript"`
				}
				if err := events.ReadJSON(&event); err != nil {
					t.Fatal(err)
				}
				if event.Type == "transcript" {
					partials++
					if event.Transcript.Confirmed != "今日は" {
						t.Fatal("intermediate prefix changed")
					}
					if partials == 2 && event.Transcript.Draft != "いい天気です。" {
						t.Fatal("intermediate preview was concatenated incorrectly")
					}
				}
			}
			cloudStub.mu.Lock()
			requestsBeforeFinal := len(cloudStub.translations)
			cloudStub.mu.Unlock()
			if requestsBeforeFinal != 0 {
				t.Fatal("draft text was translated")
			}
			if err := audio.WriteJSON(map[string]string{"type": "audio.end"}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-manager.Get(created.SessionID).Done():
			case <-time.After(5 * time.Second):
				t.Fatal("tail drain did not finish")
			}
			response, body = client.request("GET", "/v1/sessions/"+created.SessionID)
			var final caption.Snapshot
			json.Unmarshal(body, &final)
			if response.StatusCode != 200 || final.State != "stopped" || final.AudioMS != 200 || !cloudStub.finish.Load() {
				t.Fatalf("session not fully drained: %s", body)
			}
			if len(final.Captions) != 1 || len(final.Captions[0].ItemIDs) != 2 {
				t.Fatalf("final duplicate or batching incorrect: %s", body)
			}
			cloudStub.mu.Lock()
			byteCount := len(cloudStub.pcm)
			requestCount := len(cloudStub.translations)
			source := cloudStub.translations[0]
			cloudStub.mu.Unlock()
			if byteCount != 6400 || requestCount != 1 || source != "今日はいい天気です。\n明日も晴れるでしょう。" {
				t.Fatal("audio bytes or final source text incorrect")
			}
			if translationStatus == 0 {
				if final.Captions[0].Chinese != "今天天气很好。明天也会晴朗。" || final.InputTokens != 30 || final.OutputTokens != 20 {
					t.Fatalf("translation or usage missing: %s", body)
				}
			} else if final.Captions[0].State != "translation_failed" || !strings.Contains(final.Captions[0].Error, "429") {
				t.Fatal("rate limit must remain visible with Japanese preserved")
			}
			response, _ = client.request("POST", "/v1/sessions/"+created.SessionID+"/stop")
			if response.StatusCode != 200 {
				t.Fatal("stop must be idempotent")
			}
		})
	}
}

func TestMissingKeyAndOriginIsolation(t *testing.T) {
	cloudStub := newCloudFixture(t)
	manager := fixtureManager(t, cloudStub)
	app := httptest.NewServer(server.New(manager, false))
	defer app.Close()
	client := bootstrap(t, app.URL)
	response, body := client.request("POST", "/v1/sessions")
	if response.StatusCode != 503 || !strings.Contains(string(body), "DASHSCOPE_API_KEY") {
		t.Fatal("missing key must be explicit")
	}
	request, _ := http.NewRequest("POST", app.URL+"/v1/sessions", strings.NewReader("{}"))
	request.Header.Set("Origin", "https://unrelated.example")
	response, err := client.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("cross-origin request accepted")
	}
	request, _ = http.NewRequest("GET", app.URL+"/", nil)
	request.Host = "attacker.example"
	response, err = client.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("DNS rebinding host accepted")
	}
}

func TestRejectedCloudKeyFailsSessionWithoutLeakingSecrets(t *testing.T) {
	cloudStub := newCloudFixture(t)
	cloudStub.asrStatus = 401
	manager := fixtureManager(t, cloudStub)
	app := httptest.NewServer(server.New(manager, true))
	defer app.Close()
	client := bootstrap(t, app.URL)
	_, body := client.request("POST", "/v1/sessions")
	var created caption.Snapshot
	json.Unmarshal(body, &created)
	audio := client.socket("/v1/sessions/" + created.SessionID + "/audio")
	var errorEvent map[string]string
	if err := audio.ReadJSON(&errorEvent); err != nil {
		t.Fatal(err)
	}
	if errorEvent["type"] != "error" || !strings.Contains(errorEvent["message"], "401") {
		t.Fatal("missing cloud authentication error")
	}
	_, body = client.request("GET", "/v1/sessions/"+created.SessionID)
	var final caption.Snapshot
	json.Unmarshal(body, &final)
	if final.State != "failed" {
		t.Fatal("cloud rejection did not end session")
	}
}
