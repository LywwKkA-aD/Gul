package livekit

import (
	"errors"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/session"
	"github.com/pion/rtp"
)

type VoiceStats struct{ RXDrops, TXDrops, TXOffline, TXErrors uint64 }
type outgoing struct {
	epoch  uint64
	packet *rtp.Packet
}
type voiceIO struct {
	manager                               *Manager
	rx                                    chan session.VoicePacket
	tx                                    chan outgoing
	stop                                  chan struct{}
	done                                  chan struct{}
	once                                  sync.Once
	mu                                    sync.Mutex
	clock                                 txClock
	clockEpoch                            uint64
	rxDrops, txDrops, txOffline, txErrors atomic.Uint64
}

func newVoiceIO(m *Manager) *voiceIO {
	v := &voiceIO{manager: m, rx: make(chan session.VoicePacket, 256), tx: make(chan outgoing, 8), stop: make(chan struct{}), done: make(chan struct{})}
	go v.sendLoop()
	return v
}
func (m *Manager) VoicePackets() <-chan session.VoicePacket { return m.voice.rx }
func (m *Manager) VoiceStats() VoiceStats {
	return VoiceStats{m.voice.rxDrops.Load(), m.voice.txDrops.Load(), m.voice.txOffline.Load(), m.voice.txErrors.Load()}
}
func (m *Manager) VoiceDrops() uint64 { return m.voice.rxDrops.Load() }
func (m *Manager) SendVoice(opus []byte, final bool) error {
	if len(opus) == 0 {
		if final {
			return nil
		}
		return errors.New("LiveKit: пустой голосовой пакет")
	}
	m.mu.Lock()
	epoch := m.epoch
	ready := m.media != nil && m.status.State == domain.StateConnected && !m.desired.Muted && !m.closed
	m.mu.Unlock()
	if !ready {
		m.voice.txOffline.Add(1)
		return nil
	}
	v := m.voice
	v.mu.Lock()
	if v.clockEpoch != epoch {
		v.clock = newTXClock(uint16(rand.Uint32()), rand.Uint32(), time.Now())
		v.clockEpoch = epoch
	}
	packet := v.clock.packet(opus, final, time.Now())
	v.mu.Unlock()
	pushOldest(v.tx, outgoing{epoch: epoch, packet: packet}, &v.txDrops)
	return nil
}
func (v *voiceIO) receive(p session.VoicePacket) { pushOldest(v.rx, p, &v.rxDrops) }
func (v *voiceIO) sendLoop() {
	defer close(v.done)
	for {
		select {
		case <-v.stop:
			return
		case frame := <-v.tx:
			m := v.manager
			m.mu.Lock()
			media := m.media
			ready := media != nil && m.epoch == frame.epoch && m.status.State == domain.StateConnected && !m.desired.Muted && !m.closed
			m.mu.Unlock()
			if !ready {
				v.txOffline.Add(1)
				continue
			}
			if err := media.write(frame.packet); err != nil {
				v.txErrors.Add(1)
			}
		}
	}
}
func (v *voiceIO) flushTX() {
	for {
		select {
		case <-v.tx:
		default:
			return
		}
	}
}
func (v *voiceIO) flush() {
	v.flushTX()
	for {
		select {
		case <-v.rx:
		default:
			v.receive(session.VoicePacket{Reset: true})
			return
		}
	}
}
func (v *voiceIO) close() { v.once.Do(func() { close(v.stop) }); <-v.done; v.flush() }
func pushOldest[T any](ch chan T, item T, drops *atomic.Uint64) {
	for range 4 {
		select {
		case ch <- item:
			return
		default:
		}
		select {
		case <-ch:
			drops.Add(1)
		default:
		}
	}
	drops.Add(1)
}
