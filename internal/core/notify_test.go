package core

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/mumble"
)

// ---------------------------------------------------------------------------
// doubles
// ---------------------------------------------------------------------------

type posted struct{ title, body string }

type fakeNotifier struct {
	mu   sync.Mutex
	sent []posted
	err  error
}

func (f *fakeNotifier) Notify(title, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, posted{title, body})
	return f.err
}

func (f *fakeNotifier) all() []posted {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]posted(nil), f.sent...)
}

// await waits for the delivery goroutine. Notifications are posted off the
// Mumble read loop on purpose, so every assertion here is about another
// goroutine's work.
func (f *fakeNotifier) await(t *testing.T, want int) []posted {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := f.all()
		if len(got) >= want {
			return got
		}
		if time.Now().After(deadline) {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// settle gives a notification that should NOT have been sent time to appear.
func (f *fakeNotifier) settle() { time.Sleep(120 * time.Millisecond) }

// newNotifyApp builds a connected core in a channel, with a notifier attached
// and the window out of sight.
func newNotifyApp(t *testing.T) (*App, *fakeNotifier) {
	t.Helper()
	app, _, _ := newTestApp(t)
	n := &fakeNotifier{}
	app.SetNotifier(n)
	app.SetWindowState(false, false)
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected, SelfChannel: 10})
	return app, n
}

// ---------------------------------------------------------------------------
// the rule
// ---------------------------------------------------------------------------

func TestChatMessageNotifiesWhenTheWindowIsHidden(t *testing.T) {
	t.Parallel()
	app, n := newNotifyApp(t)

	app.HandleMessage(mumble.RawMessage{ChannelID: 10, Sender: "Аня", HTML: "привет,&nbsp;<b>все</b>"})

	got := n.await(t, 1)
	if len(got) != 1 {
		t.Fatalf("sent = %+v, want one notification", got)
	}
	if got[0].title != "Аня" {
		t.Errorf("title = %q, want the sender", got[0].title)
	}
	// The body is text, not markup: a notification centre is not a webview.
	if got[0].body != "привет, все" {
		t.Errorf("body = %q, want the words without the tags", got[0].body)
	}
}

// The one thing this feature may never do.
func TestNothingIsSentWhileTheWindowIsFocused(t *testing.T) {
	t.Parallel()
	app, n := newNotifyApp(t)
	app.SetWindowState(true, true)

	app.HandleMessage(mumble.RawMessage{ChannelID: 10, Sender: "Аня", HTML: "привет"})
	app.HandleTree(treeWithMembers(10, "Боря"))

	n.settle()
	if got := n.all(); len(got) != 0 {
		t.Fatalf("notified over a window the user is looking at: %+v", got)
	}
}

func TestJoinNotifiesAndLeaveDoesNot(t *testing.T) {
	t.Parallel()
	app, n := newNotifyApp(t)

	// Baseline first: the first snapshot of a session announces nobody.
	app.HandleTree(treeWithMembers(10))
	app.HandleTree(treeWithMembers(10, "Боря"))
	got := n.await(t, 1)
	if len(got) != 1 {
		t.Fatalf("sent = %+v, want one notification for the arrival", got)
	}
	if got[0].title != "Боря" || got[0].body == "" {
		t.Errorf("notification = %+v", got[0])
	}

	// Leaving keeps its cue and says nothing here.
	app.HandleTree(treeWithMembers(10))
	n.settle()
	if got := n.all(); len(got) != 1 {
		t.Fatalf("sent = %+v, want the arrival only", got)
	}
}

// Our own words are echoed locally because the server never sends them back.
// They must not notify us.
func TestOurOwnMessageDoesNotNotify(t *testing.T) {
	t.Parallel()
	app, n := newNotifyApp(t)
	ctrl := &fakeController{}
	app.SetController(ctrl)

	if err := app.SendMessage(10, "сам себе"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	n.settle()
	if got := n.all(); len(got) != 0 {
		t.Fatalf("notified about our own message: %+v", got)
	}
}

func TestAMessageForAnotherChannelDoesNotNotify(t *testing.T) {
	t.Parallel()
	app, n := newNotifyApp(t)

	app.HandleMessage(mumble.RawMessage{ChannelID: 77, Sender: "Аня", HTML: "в другой комнате"})
	n.settle()
	if got := n.all(); len(got) != 0 {
		t.Fatalf("notified about another channel: %+v", got)
	}
}

// A busy channel must not fill the notification centre. The exact bucket is
// internal/notify's business; what is pinned here is that core applies it.
func TestABusyChannelIsRateLimited(t *testing.T) {
	t.Parallel()
	app, n := newNotifyApp(t)

	for range 30 {
		app.HandleMessage(mumble.RawMessage{ChannelID: 10, Sender: "Аня", HTML: "сообщение"})
	}
	n.settle()
	got := n.all()
	if len(got) == 0 {
		t.Fatal("30 messages produced no notification at all")
	}
	if len(got) > 5 {
		t.Fatalf("%d notifications for 30 messages, want a rate limit", len(got))
	}
}

// A message with nothing left after the markup is dropped says nothing.
func TestAnEmptyMessageIsNotNotified(t *testing.T) {
	t.Parallel()
	app, n := newNotifyApp(t)

	app.HandleMessage(mumble.RawMessage{ChannelID: 10, Sender: "Аня", HTML: "<script>alert(1)</script>"})
	n.settle()
	if got := n.all(); len(got) != 0 {
		t.Fatalf("notified about a message with no words: %+v", got)
	}
}

// A notifier that fails is a courtesy that did not land, not an error path.
func TestAFailingNotifierChangesNothing(t *testing.T) {
	t.Parallel()
	app, n := newNotifyApp(t)
	n.mu.Lock()
	n.err = errors.New("no bundle identifier")
	n.mu.Unlock()

	app.HandleMessage(mumble.RawMessage{ChannelID: 10, Sender: "Аня", HTML: "привет"})
	if got := n.await(t, 1); len(got) != 1 {
		t.Fatalf("sent = %+v", got)
	}
	// The message still reached the UI and the history.
	if hist := app.History(10); len(hist) != 1 {
		t.Fatalf("history = %+v, want the message regardless", hist)
	}
}

// A core with no notifier (every test that does not ask for one, and a machine
// where notifications do not work) must simply do nothing.
func TestWithoutANotifierNothingHappens(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestApp(t)
	app.SetWindowState(false, false)
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected, SelfChannel: 10})
	app.HandleMessage(mumble.RawMessage{ChannelID: 10, Sender: "Аня", HTML: "привет"})
}

// treeWithMembers builds a root plus one channel holding self and the named
// others, which is the shape the cue diff reads.
func treeWithMembers(channelID uint32, names ...string) domain.ChannelNode {
	node := domain.ChannelNode{ID: channelID, Name: "комната"}
	node.Users = append(node.Users, domain.UserInfo{
		Session: 1, Name: "я", ChannelID: channelID, IsSelf: true,
	})
	for i, name := range names {
		node.Users = append(node.Users, domain.UserInfo{
			Session: uint32(100 + i), Name: name, ChannelID: channelID,
		})
	}
	return domain.ChannelNode{ID: 0, Name: "Root", Children: []domain.ChannelNode{node}}
}

// ---------------------------------------------------------------------------
// text
// ---------------------------------------------------------------------------

func TestPlainText(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"привет": "привет",
		"<b>жирный</b> и <i>косой</i>":        "жирный и косой",
		"первая<br/>вторая":                   "первая вторая",
		"первая<br>вторая":                    "первая вторая",
		"&lt;тег&gt; &amp; амперсанд":         "<тег> & амперсанд",
		`<a href="https://x.test">ссылка</a>`: "ссылка",
		"<script>alert(1)</script>текст":      "текст",
		"  много   \n пробелов  ":             "много пробелов",
		"":                                    "",
		"<b></b>":                             "",
	}
	for in, want := range tests {
		if got := PlainText(in); got != want {
			t.Errorf("PlainText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncateRunes(t *testing.T) {
	t.Parallel()
	if got := truncateRunes("привет", 10); got != "привет" {
		t.Errorf("short string = %q", got)
	}
	// Counted in runes: cutting Cyrillic by bytes would produce mojibake.
	got := truncateRunes("привет мир", 6)
	if got != "привет…" {
		t.Errorf("truncated = %q, want %q", got, "привет…")
	}
	if got := displayName("   "); got != "Участник" {
		t.Errorf("nameless participant = %q", got)
	}
}
