// liveprobe performs a bounded real-cloud LiveTranslate test with a local WAV.
package main

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/gorilla/websocket"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	keyPath := flag.String("key-file", "", "optional private key file; otherwise DASHSCOPE_API_KEY")
	wav := flag.String("wav", "", "16 kHz mono PCM16 WAV")
	endpoint := flag.String("url", "wss://dashscope.aliyuncs.com/api-ws/v1/realtime?model=qwen3.8-livetranslate-flash-realtime", "cloud endpoint")
	flag.Parse()
	key := strings.TrimSpace(os.Getenv("DASHSCOPE_API_KEY"))
	if *keyPath != "" {
		b, e := os.ReadFile(*keyPath)
		if e != nil {
			fmt.Println("cannot read key file")
			os.Exit(1)
		}
		key = strings.TrimSpace(string(b))
	}
	result := map[string]any{"model": "qwen3.8-livetranslate-flash-realtime", "success": false}
	err := probe(key, *wav, *endpoint, result)
	if err != nil {
		result["error"] = strings.ReplaceAll(err.Error(), key, "[REDACTED]")
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
	os.MkdirAll("test-results", 0755)
	os.WriteFile("test-results/livetranslate-probe.json", data, 0600)
	if err != nil {
		os.Exit(1)
	}
}

func probe(key, path, endpoint string, result map[string]any) error {
	if key == "" {
		return fmt.Errorf("missing API Key")
	}
	wav, e := os.ReadFile(path)
	if e != nil {
		return fmt.Errorf("cannot read test audio")
	}
	if len(wav) < 12 || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return fmt.Errorf("expected WAV")
	}
	var pcm []byte
	valid := false
	for off := 12; off+8 <= len(wav); {
		n := int(binary.LittleEndian.Uint32(wav[off+4:]))
		start := off + 8
		if n < 0 || start+n > len(wav) {
			return fmt.Errorf("invalid WAV chunk")
		}
		if string(wav[off:off+4]) == "fmt " && n >= 16 {
			b := wav[start:]
			valid = binary.LittleEndian.Uint16(b) == 1 && binary.LittleEndian.Uint16(b[2:]) == 1 && binary.LittleEndian.Uint32(b[4:]) == 16000 && binary.LittleEndian.Uint16(b[14:]) == 16
		}
		if string(wav[off:off+4]) == "data" {
			pcm = wav[start : start+n]
		}
		off = start + n + n%2
	}
	if !valid || len(pcm) == 0 {
		return fmt.Errorf("WAV must be mono 16000 Hz PCM16")
	}
	result["audio_ms"] = len(pcm) * 1000 / 32000
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dial := websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: 15 * time.Second}
	conn, resp, e := dial.DialContext(ctx, endpoint, http.Header{"Authorization": {"Bearer " + key}})
	if e != nil {
		if resp != nil {
			defer resp.Body.Close()
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			return fmt.Errorf("handshake HTTP %d: %s", resp.StatusCode, b)
		}
		return fmt.Errorf("connection failed: %v", e)
	}
	defer conn.Close()
	conn.SetReadLimit(2 << 20)
	conn.SetReadDeadline(time.Now().Add(55 * time.Second))
	var first map[string]any
	if e = conn.ReadJSON(&first); e != nil {
		return e
	}
	result["first_event"] = first["type"]
	update := map[string]any{"type": "session.update", "session": map[string]any{"voice": "Tina", "enable_voice_clone": false, "output_modalities": []string{"text"}, "audio": map[string]any{"output": map[string]any{"voice": "Tina"}, "input": map[string]any{"format": map[string]any{"type": "pcm", "sample_rate": 16000}, "turn_detection": map[string]any{"type": "server_vad", "threshold": 0.2, "silence_duration_ms": 600}}}, "translation": map[string]any{"language": "zh"}}}
	if e = conn.WriteJSON(update); e != nil {
		return e
	}
	for {
		var event map[string]any
		if e = conn.ReadJSON(&event); e != nil {
			return e
		}
		if event["type"] == "error" {
			b, _ := json.Marshal(event["error"])
			return fmt.Errorf("configuration rejected: %s", b)
		}
		if event["type"] == "session.updated" {
			result["configured"] = true
			break
		}
	}
	sendError := make(chan error, 1)
	go func() {
		audio := append(append([]byte{}, pcm...), make([]byte, 64000)...)
		for off := 0; off < len(audio); off += 3200 {
			end := off + 3200
			if end > len(audio) {
				end = len(audio)
			}
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := conn.WriteJSON(map[string]any{"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(audio[off:end])}); err != nil {
				sendError <- err
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		sendError <- conn.WriteJSON(map[string]string{"type": "session.finish"})
	}()
	counts := map[string]int{}
	var originals, translations []string
	started := time.Now()
	for {
		var event struct {
			Type       string          `json:"type"`
			Transcript string          `json:"transcript"`
			Text       string          `json:"text"`
			Error      json.RawMessage `json:"error"`
			Response   struct {
				Status string          `json:"status"`
				Usage  json.RawMessage `json:"usage"`
			} `json:"response"`
		}
		if e = conn.ReadJSON(&event); e != nil {
			return fmt.Errorf("receive failed: %v", e)
		}
		counts[event.Type]++
		if event.Type == "error" {
			return fmt.Errorf("cloud error: %s", event.Error)
		}
		if event.Type == "conversation.item.input_audio_transcription.completed" {
			originals = append(originals, event.Transcript)
		}
		if event.Type == "response.text.done" {
			translations = append(translations, event.Text)
		}
		if event.Type == "response.done" {
			result["response_status"] = event.Response.Status
			result["usage"] = event.Response.Usage
		}
		result["events"] = counts
		result["japanese"] = originals
		result["chinese"] = translations
		if event.Type == "session.finished" {
			result["elapsed_ms"] = time.Since(started).Milliseconds()
			result["success"] = len(originals) > 0 && len(translations) > 0
			if len(originals) == 0 || len(translations) == 0 {
				return fmt.Errorf("session finished without both source and translated text")
			}
			return nil
		}
	}
}
