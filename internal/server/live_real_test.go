package server_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"livecaptions/internal/caption"
	"livecaptions/internal/cloud"
	"livecaptions/internal/server"
)

// Opt-in, billable real-cloud test. Default test runs never read a real key.
func TestRealLiveTranslatePipeline(t *testing.T) {
	if os.Getenv("CAPTION_REAL_TEST") != "1" {
		t.Skip("requires explicitly authorized real key and WAV")
	}
	keyData, err := os.ReadFile(os.Getenv("CAPTION_REAL_KEY_FILE"))
	if err != nil {
		t.Fatal("cannot read private key file")
	}
	wav, err := os.ReadFile(os.Getenv("CAPTION_REAL_WAV"))
	if err != nil {
		t.Fatal("cannot read WAV")
	}
	var pcm []byte
	for off := 12; off+8 <= len(wav); {
		n := int(binary.LittleEndian.Uint32(wav[off+4:]))
		start := off + 8
		if start+n > len(wav) {
			t.Fatal("invalid WAV")
		}
		if string(wav[off:off+4]) == "data" {
			pcm = append([]byte{}, wav[start:start+n]...)
		}
		off = start + n + n%2
	}
	if len(pcm) == 0 {
		t.Fatal("empty WAV")
	}
	pcm = append(pcm, make([]byte, 64000)...)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	opts := caption.DefaultOptions()
	opts.DirectTranslation = true
	opts.FinishTimeout = 20 * time.Second
	manager := caption.NewManager(ctx, func(ctx context.Context) (caption.ASRStream, error) {
		return cloud.OpenASR(ctx, cloud.ASRConfig{URL: "wss://dashscope.aliyuncs.com/api-ws/v1/realtime?model=qwen3.8-livetranslate-flash-realtime", APIKey: strings.TrimSpace(string(keyData)), LiveTranslate: true})
	}, nil, opts)
	app := httptest.NewServer(server.New(manager, true, "livetranslate"))
	defer app.Close()
	client := bootstrap(t, app.URL)
	response, body := client.request("POST", "/v1/sessions")
	if response.StatusCode != 201 {
		t.Fatal("create failed")
	}
	var created caption.Snapshot
	json.Unmarshal(body, &created)
	audio := client.socket("/v1/sessions/" + created.SessionID + "/audio")
	audio.SetReadDeadline(time.Now().Add(20 * time.Second))
	var ready map[string]string
	if err = audio.ReadJSON(&ready); err != nil || ready["type"] != "ready" {
		t.Fatal("real model handshake failed")
	}
	started := time.Now()
	for off, seq := 0, uint32(0); off < len(pcm); off, seq = off+3200, seq+1 {
		end := off + 3200
		if end > len(pcm) {
			end = len(pcm)
		}
		frame := make([]byte, 12+end-off)
		binary.LittleEndian.PutUint32(frame, seq)
		binary.LittleEndian.PutUint64(frame[4:], uint64(off/2))
		copy(frame[12:], pcm[off:end])
		if err = audio.WriteMessage(websocket.BinaryMessage, frame); err != nil {
			t.Fatalf("audio send failed: %+v", manager.Get(created.SessionID).Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	audio.WriteJSON(map[string]string{"type": "audio.end"})
	select {
	case <-manager.Get(created.SessionID).Done():
	case <-ctx.Done():
		t.Fatal("real model timeout")
	}
	snapshot := manager.Get(created.SessionID).Snapshot()
	if snapshot.State != "stopped" || len(snapshot.Captions) == 0 || snapshot.Captions[0].Chinese == "" || snapshot.Captions[0].Japanese == "" {
		t.Fatalf("incomplete real model result: state=%s captions=%d error=%s", snapshot.State, len(snapshot.Captions), snapshot.Error)
	}
	report := map[string]any{"model": "qwen3.8-livetranslate-flash-realtime", "success": true, "elapsed_ms": time.Since(started).Milliseconds(), "snapshot": snapshot}
	data, _ := json.MarshalIndent(report, "", "  ")
	os.MkdirAll("../../test-results", 0755)
	os.WriteFile("../../test-results/livetranslate-app-real.json", data, 0600)
	t.Logf("real local HTTP/WS -> Go -> LiveTranslate -> Japanese + Chinese: %s / %s", snapshot.Captions[0].Japanese, snapshot.Captions[0].Chinese)
}
