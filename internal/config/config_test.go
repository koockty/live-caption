package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigEndpointsAndLoopback(t *testing.T) {
	t.Setenv("CAPTION_PIPELINE", "asr_mt")
	t.Setenv("DASHSCOPE_API_KEY", "test-only-key")
	t.Setenv("DASHSCOPE_WORKSPACE_ID", "llm-example")
	t.Setenv("DASHSCOPE_REGION", "cn-beijing")
	t.Setenv("CAPTION_LISTEN", "127.0.0.1:17831")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ASRURL != "wss://llm-example.cn-beijing.maas.aliyuncs.com/api-ws/v1/realtime?model=qwen3-asr-flash-realtime" {
		t.Fatal("workspace ASR endpoint incorrect")
	}
	if !strings.HasPrefix(cfg.TranslateURL, "https://llm-example.cn-beijing.maas.aliyuncs.com/") {
		t.Fatal("translation region mismatch")
	}
	t.Setenv("DASHSCOPE_WORKSPACE_ID", "")
	t.Setenv("DASHSCOPE_REGION", "ap-southeast-1")
	cfg, err = Load()
	if err != nil || !strings.HasPrefix(cfg.ASRURL, "wss://dashscope-intl.aliyuncs.com/") {
		t.Fatal("Singapore fallback endpoint incorrect")
	}
	t.Setenv("CAPTION_LISTEN", "0.0.0.0:17831")
	if _, err := Load(); err == nil {
		t.Fatal("public bind must be rejected")
	}
}

func TestEnvFileIsLiteralAndExportedValuesWin(t *testing.T) {
	t.Setenv("CAPTION_TEST_EXISTING", "already-exported")
	key := "CAPTION_TEST_LITERAL_VALUE"
	old, existed := os.LookupEnv(key)
	os.Unsetenv(key)
	t.Cleanup(func() {
		if existed {
			os.Setenv(key, old)
		} else {
			os.Unsetenv(key)
		}
	})
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("# local config\nCAPTION_TEST_EXISTING=ignored\nCAPTION_TEST_LITERAL_VALUE='$(must-not-run) `literal`'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := LoadEnvFile(path); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("CAPTION_TEST_EXISTING") != "already-exported" {
		t.Fatal("exported value overridden")
	}
	if os.Getenv(key) != "$(must-not-run) `literal`" {
		t.Fatal("env input was transformed")
	}
}
