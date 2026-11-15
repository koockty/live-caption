package caption

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestDirectTranslationDeltasAndFinalAssociation(t *testing.T) {
	s := &Session{state: "running", opts: Options{DirectTranslation: true}, liveSources: map[string]string{"source-1": "今日はいい天気です。"}, subscribers: make(map[chan Event]struct{})}
	s.acceptTranslation(ASREvent{Type: "translation.delta", ItemID: "target-1", SourceItemID: "source-1", Delta: "今天"})
	s.acceptTranslation(ASREvent{Type: "translation.delta", ItemID: "target-1", SourceItemID: "source-1", Delta: "天气很好。"})
	if len(s.captions) != 1 || s.captions[0].Chinese != "今天天气很好。" {
		t.Fatal("translation deltas did not accumulate into one subtitle")
	}
	s.acceptTranslation(ASREvent{Type: "translation.done", ItemID: "target-1", SourceItemID: "source-1", Text: "今天天气很好。"})
	s.acceptTranslation(ASREvent{Type: "translation.done", ItemID: "target-1", SourceItemID: "source-1", Text: "今天天气很好。"})
	if s.captions[0].Japanese != "今日はいい天気です。" || s.captions[0].State != "translated" || s.captions[0].Revision != 3 {
		t.Fatal("final source association or duplicate suppression failed")
	}
}

func audioFrame(seq uint32, first uint64, samples int) []byte {
	frame := make([]byte, FrameHeaderBytes+samples*2)
	binary.LittleEndian.PutUint32(frame, seq)
	binary.LittleEndian.PutUint64(frame[4:], first)
	return frame
}

func TestAudioSequencePartialTailAndBoundedBuffer(t *testing.T) {
	s := &Session{state: "running", audio: make(chan []byte, 10)}
	if err := s.PushFrame(audioFrame(0, 0, FrameSamples)); err != nil {
		t.Fatal(err)
	}
	if err := s.PushFrame(audioFrame(0, 1600, FrameSamples)); err == nil {
		t.Fatal("repeated frame sequence accepted")
	}
	if err := s.PushFrame(audioFrame(1, 1599, FrameSamples)); err == nil {
		t.Fatal("sample gap accepted")
	}
	if err := s.PushFrame(audioFrame(1, 1600, 128)); err != nil {
		t.Fatal("partial last frame rejected")
	}
	if err := s.PushFrame(audioFrame(2, 1728, FrameSamples)); err == nil {
		t.Fatal("audio after partial tail accepted")
	}
	if s.samples != 1728 {
		t.Fatal("rejected frames changed the capture clock")
	}

	s = &Session{state: "running", audio: make(chan []byte, 10)}
	for i := uint32(0); i < 10; i++ {
		if err := s.PushFrame(audioFrame(i, uint64(i)*1600, FrameSamples)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PushFrame(audioFrame(10, 16000, FrameSamples)); !errors.Is(err, ErrBackpressure) {
		t.Fatal("one-second buffer limit not enforced")
	}
	if s.samples != 16000 {
		t.Fatal("rejected backlog was counted as sent")
	}
}
