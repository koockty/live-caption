package config

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
)

type Config struct {
	Pipeline     string
	Listen       string
	APIKey       string
	WorkspaceID  string
	Region       string
	ASRURL       string
	TranslateURL string
}

// LoadEnvFile accepts literal KEY=value lines, never evaluates shell expressions,
// and leaves variables already exported by the launching process unchanged.
func LoadEnvFile(path string) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	line := 0
	for scanner.Scan() {
		line++
		v := strings.TrimSpace(scanner.Text())
		if v == "" || strings.HasPrefix(v, "#") {
			continue
		}
		v = strings.TrimPrefix(v, "export ")
		key, value, ok := strings.Cut(v, "=")
		key = strings.TrimSpace(key)
		if !ok || !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(key) {
			return fmt.Errorf(".env 第 %d 行格式无效，应为 KEY=value", line)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"')) {
			value = value[1 : len(value)-1]
		}
		if _, present := os.LookupEnv(key); !present {
			if err := os.Setenv(key, value); err != nil {
				return fmt.Errorf("设置 .env 第 %d 行失败", line)
			}
		}
	}
	return scanner.Err()
}

func Load() (Config, error) {
	c := Config{
		Pipeline:    env("CAPTION_PIPELINE", "livetranslate"),
		Listen:      env("CAPTION_LISTEN", "127.0.0.1:17831"),
		APIKey:      strings.TrimSpace(os.Getenv("DASHSCOPE_API_KEY")),
		WorkspaceID: strings.TrimSpace(os.Getenv("DASHSCOPE_WORKSPACE_ID")),
		Region:      env("DASHSCOPE_REGION", "cn-beijing"),
	}
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return Config{}, errors.New("CAPTION_LISTEN 必须为回环地址，例如 127.0.0.1:17831")
	}
	if c.Region != "cn-beijing" && c.Region != "ap-southeast-1" {
		return Config{}, errors.New("DASHSCOPE_REGION 仅支持 cn-beijing 或 ap-southeast-1")
	}
	if c.WorkspaceID != "" && !regexp.MustCompile(`^[a-zA-Z0-9-]+$`).MatchString(c.WorkspaceID) {
		return Config{}, errors.New("DASHSCOPE_WORKSPACE_ID 只能包含字母、数字和连字符")
	}
	domain := "dashscope.aliyuncs.com"
	if c.Region == "ap-southeast-1" {
		domain = "dashscope-intl.aliyuncs.com"
	}
	if c.WorkspaceID != "" {
		domain = c.WorkspaceID + "." + c.Region + ".maas.aliyuncs.com"
	}
	model := "qwen3-asr-flash-realtime"
	if c.Pipeline == "livetranslate" {
		model = "qwen3.8-livetranslate-flash-realtime"
	} else if c.Pipeline != "asr_mt" {
		return Config{}, errors.New("CAPTION_PIPELINE 仅支持 livetranslate 或 asr_mt")
	}
	c.ASRURL = "wss://" + domain + "/api-ws/v1/realtime?model=" + model
	c.TranslateURL = "https://" + domain + "/compatible-mode/v1/chat/completions"
	return c, nil
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
