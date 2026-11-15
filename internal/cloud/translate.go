package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"livecaptions/internal/caption"
)

type Translator struct {
	URL    string
	APIKey string
	Client *http.Client
}

func (t *Translator) Translate(ctx context.Context, source string, memory []caption.Memory) (caption.Translation, error) {
	options := map[string]any{"source_lang": "Japanese", "target_lang": "Chinese"}
	if len(memory) != 0 {
		options["tm_list"] = memory
	}
	data, err := json.Marshal(map[string]any{
		"model": "qwen-mt-flash", "stream": false,
		"messages":            []map[string]string{{"role": "user", "content": source}},
		"translation_options": options,
	})
	if err != nil {
		return caption.Translation{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, t.URL, bytes.NewReader(data))
	if err != nil {
		return caption.Translation{}, fmt.Errorf("翻译服务地址无效")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+t.APIKey)
	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	}
	response, err := client.Do(request)
	if err != nil {
		return caption.Translation{}, fmt.Errorf("翻译请求超时或连接失败")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return caption.Translation{}, fmt.Errorf("翻译服务返回 HTTP %d，请检查 Key、地域、额度或限流", response.StatusCode)
	}
	var body struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			Input  int `json:"prompt_tokens"`
			Output int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&body); err != nil || len(body.Choices) == 0 {
		return caption.Translation{}, fmt.Errorf("翻译服务返回内容无效")
	}
	if body.Choices[0].FinishReason == "length" {
		return caption.Translation{}, fmt.Errorf("译文被模型截断，请重新识别较短语句")
	}
	text := strings.TrimSpace(body.Choices[0].Message.Content)
	if text == "" {
		return caption.Translation{}, fmt.Errorf("翻译服务返回了空译文")
	}
	return caption.Translation{Text: text, InputTokens: body.Usage.Input, OutputTokens: body.Usage.Output}, nil
}
