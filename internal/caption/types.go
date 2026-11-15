package caption

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

const (
	SampleRate       = 16000
	FrameSamples     = 1600
	FrameBytes       = FrameSamples * 2
	FrameHeaderBytes = 12 // uint32 sequence + uint64 first sample index, little endian
)

var (
	ErrClosed       = errors.New("会话已结束")
	ErrConflict     = errors.New("已有正在运行的会话，请先停止")
	ErrAudioBusy    = errors.New("当前会话已经有音频采集连接")
	ErrBackpressure = errors.New("音频发送积压超过一秒，请重新开始")
)

type ASREvent struct {
	Delta        string `json:"delta"`
	SourceItemID string `json:"source_item_id"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	Type         string `json:"type"`
	ItemID       string `json:"item_id"`
	Text         string `json:"text"`
	Stash        string `json:"stash"`
	Transcript   string `json:"transcript"`
	Error        struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
}

type ASRStream interface {
	SendPCM([]byte) error
	Receive() (ASREvent, error)
	Finish() error
	Close() error
}

type ASRFactory func(context.Context) (ASRStream, error)

type Memory struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type Translation struct {
	Text         string
	InputTokens  int
	OutputTokens int
}

type Translator interface {
	Translate(context.Context, string, []Memory) (Translation, error)
}

type Caption struct {
	ID          string   `json:"id"`
	Order       int      `json:"order"`
	ItemIDs     []string `json:"item_ids"`
	Japanese    string   `json:"ja"`
	Chinese     string   `json:"zh"`
	State       string   `json:"state"`
	Revision    int      `json:"revision"`
	Error       string   `json:"error,omitempty"`
	TranslateMS int64    `json:"translate_ms,omitempty"`
}

type Transcript struct {
	ItemID    string `json:"item_id"`
	Confirmed string `json:"confirmed"`
	Draft     string `json:"draft"`
	Final     bool   `json:"final"`
}

type Snapshot struct {
	SessionID    string     `json:"session_id"`
	Epoch        int        `json:"epoch"`
	State        string     `json:"state"`
	EventSeq     uint64     `json:"event_seq"`
	Transcript   Transcript `json:"transcript"`
	Captions     []Caption  `json:"captions"`
	AudioMS      uint64     `json:"audio_ms"`
	InputTokens  int        `json:"input_tokens"`
	OutputTokens int        `json:"output_tokens"`
	Error        string     `json:"error,omitempty"`
}

type Event struct {
	Type       string      `json:"type"`
	SessionID  string      `json:"session_id"`
	Epoch      int         `json:"epoch"`
	Seq        uint64      `json:"seq"`
	State      string      `json:"state,omitempty"`
	Message    string      `json:"message,omitempty"`
	Caption    *Caption    `json:"caption,omitempty"`
	Transcript *Transcript `json:"transcript,omitempty"`
}

type Options struct {
	DirectTranslation   bool
	TranslationInterval time.Duration
	BatchWindow         time.Duration
	TranslationTimeout  time.Duration
	FinishTimeout       time.Duration
}

func DefaultOptions() Options {
	return Options{TranslationInterval: 1250 * time.Millisecond, BatchWindow: 120 * time.Millisecond, TranslationTimeout: 8 * time.Second, FinishTimeout: 10 * time.Second}
}

func ID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic("操作系统随机数不可用")
	}
	return hex.EncodeToString(bytes[:])
}
