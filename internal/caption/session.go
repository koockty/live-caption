package caption

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"time"
)

type textJob struct {
	itemID string
	text   string
}

type Session struct {
	ID           string
	ctx          context.Context
	cancel       context.CancelFunc
	factory      ASRFactory
	translator   Translator
	opts         Options
	mu           sync.Mutex
	state        string
	errorMessage string
	seq          uint64
	transcript   Transcript
	captions     []Caption
	captionCount int
	inputTokens  int
	outputTokens int
	expectedSeq  uint32
	samples      uint64
	partialFrame bool
	inputClosed  bool
	audio        chan []byte
	jobs         chan textJob
	asr          ASRStream
	asrDone      chan struct{}
	done         chan struct{}
	subscribers  map[chan Event]struct{}
	seenFinal    map[string]struct{}
	finalOrder   []string
	liveSources  map[string]string
	expiry       *time.Timer
}

type Manager struct {
	mu         sync.Mutex
	ctx        context.Context
	factory    ASRFactory
	translator Translator
	opts       Options
	current    *Session
}

func NewManager(ctx context.Context, factory ASRFactory, translator Translator, opts Options) *Manager {
	return &Manager{ctx: ctx, factory: factory, translator: translator, opts: opts}
}

func (m *Manager) Create() (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != nil && !m.current.Terminal() {
		return nil, ErrConflict
	}
	ctx, cancel := context.WithCancel(m.ctx)
	s := &Session{
		ID: ID(), ctx: ctx, cancel: cancel, factory: m.factory, translator: m.translator, opts: m.opts,
		state: "created", audio: make(chan []byte, 10), jobs: make(chan textJob, 64),
		asrDone: make(chan struct{}), done: make(chan struct{}),
		subscribers: make(map[chan Event]struct{}), seenFinal: make(map[string]struct{}),
		captions:    make([]Caption, 0),
		liveSources: make(map[string]string),
	}
	s.expiry = time.AfterFunc(time.Minute, func() {
		s.mu.Lock()
		unused := s.state == "created"
		s.mu.Unlock()
		if unused {
			s.EndInput()
		}
	})
	m.current = s
	go func() {
		select {
		case <-m.ctx.Done():
			s.Fail("本地服务正在关闭")
		case <-s.done:
		}
	}()
	return s, nil
}

func (m *Manager) Get(id string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != nil && m.current.ID == id {
		return m.current
	}
	return nil
}

func (m *Manager) CurrentSnapshot() *Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return nil
	}
	snapshot := m.current.Snapshot()
	return &snapshot
}

func (m *Manager) Stop() <-chan struct{} {
	m.mu.Lock()
	s := m.current
	m.mu.Unlock()
	if s != nil {
		s.EndInput()
		return s.Done()
	}
	done := make(chan struct{})
	close(done)
	return done
}

func (s *Session) Terminal() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return terminal(s.state)
}

func terminal(state string) bool { return state == "stopped" || state == "failed" }

func (s *Session) Done() <-chan struct{} { return s.done }

// Start is claimed atomically, so a second browser can never feed the same stream.
func (s *Session) Start() error {
	s.mu.Lock()
	if terminal(s.state) || s.inputClosed {
		s.mu.Unlock()
		return ErrClosed
	}
	if s.state != "created" {
		s.mu.Unlock()
		return ErrAudioBusy
	}
	s.expiry.Stop()
	s.state = "connecting"
	s.publishLocked(Event{Type: "status", State: s.state})
	s.mu.Unlock()
	stream, err := s.factory(s.ctx)
	if err != nil {
		if !s.Terminal() {
			s.Fail(err.Error())
		}
		return err
	}
	s.mu.Lock()
	if terminal(s.state) {
		s.mu.Unlock()
		stream.Close()
		return ErrClosed
	}
	s.asr = stream
	s.state = "running"
	s.publishLocked(Event{Type: "status", State: s.state})
	s.mu.Unlock()
	go s.sendAudio(stream)
	go s.receiveASR(stream)
	if !s.opts.DirectTranslation {
		go s.translate()
	}
	return nil
}

func (s *Session) PushFrame(frame []byte) error {
	if len(frame) <= FrameHeaderBytes || len(frame) > FrameHeaderBytes+FrameBytes || (len(frame)-FrameHeaderBytes)%2 != 0 {
		return fmt.Errorf("音频帧大小无效")
	}
	seq := binary.LittleEndian.Uint32(frame[:4])
	firstSample := binary.LittleEndian.Uint64(frame[4:12])
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != "running" || s.inputClosed {
		return ErrClosed
	}
	if seq != s.expectedSeq || firstSample != s.samples || s.partialFrame {
		return fmt.Errorf("音频帧不连续，请重新开始")
	}
	pcm := append([]byte(nil), frame[FrameHeaderBytes:]...)
	select {
	case s.audio <- pcm:
		s.expectedSeq++
		s.samples += uint64(len(pcm) / 2)
		s.partialFrame = len(pcm) != FrameBytes
		return nil
	default:
		return ErrBackpressure
	}
}

func (s *Session) EndInput() {
	s.mu.Lock()
	if terminal(s.state) || s.inputClosed {
		s.mu.Unlock()
		return
	}
	if s.state == "created" || s.state == "connecting" {
		s.mu.Unlock()
		s.terminate("stopped", "")
		return
	}
	s.inputClosed = true
	close(s.audio)
	s.state = "finishing"
	s.publishLocked(Event{Type: "status", State: s.state})
	s.mu.Unlock()
}

func (s *Session) sendAudio(stream ASRStream) {
	for {
		select {
		case <-s.ctx.Done():
			return
		case pcm, ok := <-s.audio:
			if !ok {
				if err := stream.Finish(); err != nil {
					s.Fail("发送语音结束事件失败")
					return
				}
				timer := time.NewTimer(s.opts.FinishTimeout)
				defer timer.Stop()
				select {
				case <-s.asrDone:
				case <-s.ctx.Done():
				case <-timer.C:
					s.Fail("等待尾句识别超时，请重新开始")
				}
				return
			}
			if err := stream.SendPCM(pcm); err != nil {
				s.Fail("音频上传失败，请检查网络并重新开始")
				return
			}
		}
	}
}

func (s *Session) receiveASR(stream ASRStream) {
	for {
		event, err := stream.Receive()
		if err != nil {
			if s.ctx.Err() == nil {
				s.Fail(err.Error())
			}
			return
		}
		switch event.Type {
		case "translation.delta", "translation.done":
			s.acceptTranslation(event)
		case "translation.usage":
			s.mu.Lock()
			s.inputTokens = event.InputTokens
			s.outputTokens = event.OutputTokens
			s.mu.Unlock()
		case "conversation.item.input_audio_transcription.text":
			s.mu.Lock()
			if !terminal(s.state) {
				s.transcript = Transcript{ItemID: event.ItemID, Confirmed: event.Text, Draft: event.Stash}
				t := s.transcript
				s.publishLocked(Event{Type: "transcript", Transcript: &t})
			}
			s.mu.Unlock()
		case "conversation.item.input_audio_transcription.completed":
			if !s.acceptFinal(event) {
				return
			}
		case "session.finished":
			s.mu.Lock()
			inputClosed := s.inputClosed
			s.mu.Unlock()
			if !inputClosed {
				s.Fail("语音服务提前结束了会话，请重新开始")
				return
			}
			close(s.asrDone)
			close(s.jobs)
			if s.opts.DirectTranslation {
				s.terminate("stopped", "")
			}
			return
		}
	}
}

func (s *Session) acceptFinal(event ASREvent) bool {
	s.mu.Lock()
	if terminal(s.state) {
		s.mu.Unlock()
		return false
	}
	if _, exists := s.seenFinal[event.ItemID]; exists {
		s.mu.Unlock()
		return true
	}
	if event.ItemID == "" {
		s.mu.Unlock()
		s.Fail("语音服务返回缺少句子标识")
		return false
	}
	s.seenFinal[event.ItemID] = struct{}{}
	s.finalOrder = append(s.finalOrder, event.ItemID)
	if len(s.finalOrder) > 128 {
		delete(s.seenFinal, s.finalOrder[0])
		s.finalOrder = s.finalOrder[1:]
	}
	s.transcript = Transcript{ItemID: event.ItemID, Confirmed: event.Transcript, Final: true}
	if s.opts.DirectTranslation {
		s.liveSources[event.ItemID] = event.Transcript
	}
	t := s.transcript
	s.publishLocked(Event{Type: "transcript", Transcript: &t})
	s.mu.Unlock()
	if s.opts.DirectTranslation {
		return true
	}
	if text := strings.TrimSpace(event.Transcript); text != "" {
		select {
		case s.jobs <- textJob{event.ItemID, text}:
		case <-s.ctx.Done():
			return false
		default:
			s.Fail("待翻译文字积压过多，请重新开始")
			return false
		}
	}
	return true
}

func (s *Session) acceptTranslation(event ASREvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if terminal(s.state) {
		return
	}
	index := -1
	for i := range s.captions {
		if s.captions[i].ID == event.ItemID {
			index = i
			break
		}
	}
	if index < 0 {
		s.captionCount++
		s.captions = append(s.captions, Caption{ID: event.ItemID, Order: s.captionCount, ItemIDs: []string{event.SourceItemID}, State: "translating"})
		index = len(s.captions) - 1
	}
	u := &s.captions[index]
	if u.State == "translated" {
		return
	}
	u.Revision++
	u.Japanese = s.liveSources[event.SourceItemID]
	if event.Type == "translation.done" {
		u.Chinese = event.Text
		u.State = "translated"
		delete(s.liveSources, event.SourceItemID)
	} else {
		u.Chinese += event.Delta
	}
	copy := *u
	s.publishLocked(Event{Type: "caption.upsert", Caption: &copy})
	if len(s.captions) > 200 {
		s.captions = s.captions[len(s.captions)-200:]
	}
}

func (s *Session) translate() {
	var lastRequest time.Time
	var memory []Memory
	for {
		var first textJob
		var ok bool
		select {
		case <-s.ctx.Done():
			return
		case first, ok = <-s.jobs:
		}
		if !ok {
			s.terminate("stopped", "")
			return
		}
		wait := s.opts.BatchWindow
		if remaining := time.Until(lastRequest.Add(s.opts.TranslationInterval)); remaining > wait {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-s.ctx.Done():
			timer.Stop()
			return
		}
		batch := []textJob{first}
	drain:
		for len(batch) < 64 {
			select {
			case job, more := <-s.jobs:
				if !more {
					break drain
				}
				batch = append(batch, job)
			default:
				break drain
			}
		}
		var texts, itemIDs []string
		for _, job := range batch {
			texts = append(texts, job.text)
			itemIDs = append(itemIDs, job.itemID)
		}
		unit := Caption{ID: ID(), Japanese: strings.Join(texts, "\n"), ItemIDs: itemIDs, State: "translating", Revision: 1}
		s.mu.Lock()
		if terminal(s.state) {
			s.mu.Unlock()
			return
		}
		s.captionCount++
		unit.Order = s.captionCount
		s.captions = append(s.captions, unit)
		if len(s.captions) > 200 {
			s.captions = s.captions[len(s.captions)-200:]
		}
		pending := unit
		s.publishLocked(Event{Type: "caption.upsert", Caption: &pending})
		s.mu.Unlock()
		ctx, cancel := context.WithTimeout(s.ctx, s.opts.TranslationTimeout)
		lastRequest = time.Now()
		result, err := s.translator.Translate(ctx, unit.Japanese, memory)
		cancel()
		unit.TranslateMS = time.Since(lastRequest).Milliseconds()
		unit.Revision++
		if err != nil {
			unit.State = "translation_failed"
			unit.Error = err.Error()
		} else {
			unit.State = "translated"
			unit.Chinese = result.Text
			memory = append(memory, Memory{Source: unit.Japanese, Target: result.Text})
			if len(memory) > 3 {
				memory = memory[len(memory)-3:]
			}
		}
		s.mu.Lock()
		if terminal(s.state) {
			s.mu.Unlock()
			return
		}
		s.inputTokens += result.InputTokens
		s.outputTokens += result.OutputTokens
		for i := range s.captions {
			if s.captions[i].ID == unit.ID {
				s.captions[i] = unit
				break
			}
		}
		finished := unit
		s.publishLocked(Event{Type: "caption.upsert", Caption: &finished})
		s.mu.Unlock()
	}
}

func (s *Session) Fail(message string) { s.terminate("failed", message) }

func (s *Session) terminate(state, message string) {
	s.mu.Lock()
	if terminal(s.state) {
		s.mu.Unlock()
		return
	}
	s.state = state
	s.errorMessage = message
	if s.expiry != nil {
		s.expiry.Stop()
	}
	s.publishLocked(Event{Type: "status", State: state, Message: message})
	close(s.done)
	s.cancel()
	stream := s.asr
	for ch := range s.subscribers {
		close(ch)
		delete(s.subscribers, ch)
	}
	s.mu.Unlock()
	if stream != nil {
		stream.Close()
	}
}

func (s *Session) publishLocked(event Event) {
	s.seq++
	event.SessionID = s.ID
	event.Epoch = 1
	event.Seq = s.seq
	for ch := range s.subscribers {
		select {
		case ch <- event:
		default:
			// A slow subscriber must reconnect for an atomic snapshot instead of
			// silently receiving captions with missing revisions.
			close(ch)
			delete(s.subscribers, ch)
		}
	}
}

func (s *Session) snapshotLocked() Snapshot {
	return Snapshot{SessionID: s.ID, Epoch: 1, State: s.state, EventSeq: s.seq,
		Transcript: s.transcript, Captions: append([]Caption{}, s.captions...), AudioMS: s.samples * 1000 / SampleRate,
		InputTokens: s.inputTokens, OutputTokens: s.outputTokens, Error: s.errorMessage}
}

func (s *Session) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *Session) Subscribe() (Snapshot, <-chan Event, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan Event, 64)
	if terminal(s.state) {
		close(ch)
	} else {
		s.subscribers[ch] = struct{}{}
	}
	return s.snapshotLocked(), ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, present := s.subscribers[ch]; present {
			delete(s.subscribers, ch)
			close(ch)
		}
	}
}
