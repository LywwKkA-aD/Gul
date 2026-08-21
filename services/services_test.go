package services

import (
	"errors"
	"sync"
	"testing"

	"gul/internal/core"
	"gul/internal/domain"
	"gul/internal/mumble"
)

// The services layer must stay a pass-through (PLAN.md §10.4). These tests
// pin the delegation: every call has to reach the controller unchanged, and
// no service may decide anything on its own.

type recorder struct {
	mu       sync.Mutex
	connects []string
	joins    []uint32
	sends    []string
	discos   int
	accepts  int
}

func (r *recorder) Connect(address, username, password string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.connects = append(r.connects, address+"|"+username+"|"+password)
}
func (r *recorder) Disconnect() { r.mu.Lock(); defer r.mu.Unlock(); r.discos++ }
func (r *recorder) Join(id uint32) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.joins = append(r.joins, id)
	return nil
}
func (r *recorder) SendMessage(_ uint32, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sends = append(r.sends, text)
	return nil
}
func (r *recorder) AcceptFingerprint()              { r.mu.Lock(); defer r.mu.Unlock(); r.accepts++ }
func (r *recorder) Status() domain.ConnectionStatus { return domain.ConnectionStatus{} }
func (r *recorder) Close()                          {}

var _ mumble.Controller = (*recorder)(nil)

type nopEmitter struct{}

func (nopEmitter) Emit(string, any) {}

func newApp(t *testing.T) (*core.App, *recorder) {
	t.Helper()
	rec := &recorder{}
	app := core.New(nil, nopEmitter{})
	app.SetController(rec)
	return app, rec
}

func TestConnectionServiceDelegates(t *testing.T) {
	t.Parallel()
	app, rec := newApp(t)
	svc := NewConnectionService(app)

	if err := svc.Connect("localhost:64738", "bob", "pw"); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := svc.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	svc.AcceptFingerprint()

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.connects) != 1 || rec.connects[0] != "localhost:64738|bob|pw" {
		t.Errorf("connects = %v", rec.connects)
	}
	if rec.discos != 1 || rec.accepts != 1 {
		t.Errorf("discos=%d accepts=%d, want 1/1", rec.discos, rec.accepts)
	}
}

func TestConnectionServicePropagatesValidationError(t *testing.T) {
	t.Parallel()
	app, rec := newApp(t)
	svc := NewConnectionService(app)

	if err := svc.Connect("localhost:64738", "  ", ""); !errors.Is(err, core.ErrEmptyUsername) {
		t.Fatalf("err = %v, want %v", err, core.ErrEmptyUsername)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.connects) != 0 {
		t.Fatalf("controller was called despite invalid input: %v", rec.connects)
	}
}

func TestConnectionServiceReportsState(t *testing.T) {
	t.Parallel()
	app, _ := newApp(t)
	svc := NewConnectionService(app)

	if got := svc.State(); got != string(domain.StateDisconnected) {
		t.Fatalf("State() = %q, want %q", got, domain.StateDisconnected)
	}
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected, Server: "s"})

	if got := svc.State(); got != string(domain.StateConnected) {
		t.Fatalf("State() = %q, want %q", got, domain.StateConnected)
	}
	if got := svc.Status(); got.Server != "s" || got.State != domain.StateConnected {
		t.Fatalf("Status() = %+v", got)
	}
}

func TestChannelsServiceDelegates(t *testing.T) {
	t.Parallel()
	app, rec := newApp(t)
	svc := NewChannelsService(app)

	if err := svc.Join(7); err != nil {
		t.Fatalf("Join: %v", err)
	}
	rec.mu.Lock()
	joins := append([]uint32(nil), rec.joins...)
	rec.mu.Unlock()
	if len(joins) != 1 || joins[0] != 7 {
		t.Fatalf("joins = %v, want [7]", joins)
	}

	app.HandleTree(domain.ChannelNode{ID: 0, Name: "Root"})
	if got := svc.Tree(); got.Name != "Root" {
		t.Fatalf("Tree() = %+v", got)
	}
}

func TestChatServiceDelegates(t *testing.T) {
	t.Parallel()
	app, rec := newApp(t)
	svc := NewChatService(app)

	if err := svc.Send(3, "  hello  "); err != nil {
		t.Fatalf("Send: %v", err)
	}
	rec.mu.Lock()
	sends := append([]string(nil), rec.sends...)
	rec.mu.Unlock()
	if len(sends) != 1 || sends[0] != "hello" {
		t.Fatalf("sends = %v, want [hello]", sends)
	}

	if err := svc.Send(3, "   "); !errors.Is(err, core.ErrEmptyMessage) {
		t.Fatalf("blank send err = %v, want %v", err, core.ErrEmptyMessage)
	}

	// A successful send is echoed into local history (the server never sends
	// our own text back), a rejected one is not.
	hist := svc.History(3)
	if len(hist) != 1 || hist[0].HTML != "hello" {
		t.Fatalf("History after send = %+v, want local echo [hello]", hist)
	}

	app.HandleMessage(mumble.RawMessage{ChannelID: 3, Sender: "bob", HTML: "<b>hi</b><script>x</script>"})
	hist = svc.History(3)
	if len(hist) != 2 || hist[1].HTML != "<b>hi</b>" {
		t.Fatalf("History = %+v", hist)
	}
	if got := svc.History(99); len(got) != 0 {
		t.Fatalf("History(99) = %+v, want empty", got)
	}
}

func TestDiagnosticsServiceIsWired(t *testing.T) {
	t.Parallel()
	app, _ := newApp(t)
	if svc := NewDiagnosticsService(app); svc == nil || svc.app != app {
		t.Fatal("DiagnosticsService not wired to the core app")
	}
}
