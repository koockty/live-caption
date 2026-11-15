package server_test

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"livecaptions/internal/server"
)

// This opt-in test is a browser harness, not a production mock mode. Its only
// substituted boundary is the cloud API. A real Chrome tab supplies real audio.
func TestInteractiveChromeCapture(t *testing.T) {
	if os.Getenv("CAPTION_BROWSER_TEST") != "1" {
		t.Skip("opt-in: CAPTION_BROWSER_TEST=1 go test -run TestInteractiveChromeCapture -v ./internal/server")
	}
	fixture := newCloudFixture(t)
	fixture.interactive = true
	manager := fixtureManager(t, fixture)
	app := server.New(manager, true)
	connected := make(chan string, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/test-tone" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, toneHTML)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/audio") {
			parts := strings.Split(r.URL.Path, "/")
			if len(parts) == 5 {
				select {
				case connected <- parts[3]:
				default:
				}
			}
		}
		if r.URL.Path == "/" {
			recorder := httptest.NewRecorder()
			app.ServeHTTP(recorder, r)
			for key, values := range recorder.Header() {
				for _, value := range values {
					w.Header().Add(key, value)
				}
			}
			w.Header().Del("Content-Length")
			w.WriteHeader(recorder.Code)
			body := bytes.Replace(recorder.Body.Bytes(), []byte("<main>"), []byte(`<main><div class="notice">仅本机浏览器测试：云端接口已替换为测试服务，以下日文和中文为固定测试数据。</div>`), 1)
			w.Write(body)
			return
		}
		app.ServeHTTP(w, r)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:17832")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second * 5}
	go httpServer.Serve(listener)
	t.Cleanup(func() { httpServer.Close() })
	t.Log("Browser harness: http://127.0.0.1:17832/ ; test source: http://127.0.0.1:17832/test-tone")
	var id string
	select {
	case id = <-connected:
	case <-time.After(8 * time.Minute):
		t.Fatal("no browser connected within 8 minutes")
	}
	s := manager.Get(id)
	if s == nil {
		t.Fatal("missing browser session")
	}
	select {
	case <-s.Done():
	case <-time.After(8 * time.Minute):
		t.Fatal("browser did not stop capture within 8 minutes")
	}
	snapshot := s.Snapshot()
	if snapshot.State != "stopped" || snapshot.AudioMS < 2000 || !fixture.signalDetected.Load() || len(snapshot.Captions) == 0 || snapshot.Captions[0].Chinese == "" {
		t.Fatalf("browser capture not verified: state=%s, audio_ms=%d, signal=%v, captions=%d", snapshot.State, snapshot.AudioMS, fixture.signalDetected.Load(), len(snapshot.Captions))
	}
	t.Logf("Verified Chrome -> AudioWorklet -> PCM16 -> local Go -> cloud protocol fixture -> captions: audio_ms=%d, nonzero_pcm=true", snapshot.AudioMS)
	// Allow the real browser to consume the final status before the harness exits.
	time.Sleep(500 * time.Millisecond)
}

const toneHTML = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>Live Captions · 音频采集测试源</title></head><body><h1>本地音频采集测试源</h1><p>此页面生成低音量 880 Hz 测试音，仅用于检查标签页音频采集。云端测试服务会返回固定字幕，不代表真实语音识别结果。</p><button id="play">播放测试音</button><button id="stop" disabled>停止测试音</button><p id="state">等待播放</p><script>let context, oscillator;document.querySelector('#play').onclick=async()=>{context=new AudioContext({sampleRate:48000});await context.resume();oscillator=context.createOscillator();oscillator.frequency.value=880;let gain=context.createGain();gain.gain.value=0.025;oscillator.connect(gain).connect(context.destination);oscillator.start();document.querySelector('#play').disabled=true;document.querySelector('#stop').disabled=false;document.querySelector('#state').textContent='正在播放 48 kHz 测试音';};document.querySelector('#stop').onclick=()=>{oscillator?.stop();context?.close();document.querySelector('#play').disabled=false;document.querySelector('#stop').disabled=true;document.querySelector('#state').textContent='已停止';};</script></body></html>`
