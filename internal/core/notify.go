package core

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LywwKkA-aD/Gul/internal/notify"
)

// System notifications for a window the user is not looking at (PLAN.md 7 M4+).
//
// What is notified: a chat message in the channel we are in, and somebody
// arriving in it. Both already have a cue that plays through the audio engine;
// the notification is the half that reaches somebody who is in another
// application, and the rate limit in internal/notify is what keeps a busy
// channel from filling the notification centre.
//
// What is never notified: anything at all while the window is on screen and
// focused, our own messages, and messages in channels we are not in (the
// server does not deliver those anyway).
//
// Delivery is a courtesy, so nothing here is allowed to matter: a machine that
// cannot notify (a bare binary on macOS, a session bus that is not there) logs
// once and carries on.

// notifyBodyRunes caps a notification body. Notification centres truncate on
// their own, at a length that differs per platform; cutting here means the
// string we hand over is the string we meant.
const notifyBodyRunes = 180

// Notifier posts one system notification. Implemented in main.go over the
// Wails notifications service; a build or a machine where notifications do not
// work supplies one that reports itself unavailable and does nothing.
type Notifier interface {
	// Notify posts a notification. It may block (a D-Bus round trip, a
	// dispatch to the main thread), so core never calls it inline on a
	// callback from the Mumble read loop.
	Notify(title, body string) error
}

// SetNotifier injects the platform notifier. Call once, before the UI runs. A
// core without one simply never notifies, which is what every test gets.
func (a *App) SetNotifier(n Notifier) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.notifier = n
}

// SetWindowState records what the application window is doing. It is called
// from the window's own event hooks in main.go, which is the only place that
// knows.
func (a *App) SetWindowState(visible, focused bool) {
	a.notifications.SetWindow(notify.Window{Visible: visible, Focused: focused})
}

// notifyMessage announces a chat message from somebody else in our channel.
// Runs on the Mumble read loop, so it does no work of its own beyond the
// decision.
func (a *App) notifyMessage(sender, html string) {
	body := PlainText(html)
	if body == "" {
		return
	}
	a.post(notify.KindMessage, displayName(sender), body)
}

// notifyChannelCue announces the arrival half of a channel diff. A departure
// keeps its cue and gets no notification: it is not something to come back to
// the window for (internal/notify).
func (a *App) notifyChannelCue(cue Cue, who string) {
	if cue != CueJoin {
		return
	}
	a.post(notify.KindJoin, displayName(who), "Теперь в вашем канале")
}

// post applies the policy and, if it says yes, hands the notification to the
// platform on a goroutine of its own.
//
// The goroutine is the point: every caller here is a Mumble callback running on
// the read loop, which has a 20-second server deadline and may not wait for a
// notification centre. The rate limit bounds how many of them can exist.
func (a *App) post(kind notify.Kind, title, body string) {
	if !a.notifications.Should(kind, time.Now()) {
		return
	}
	a.mu.Lock()
	n := a.notifier
	a.mu.Unlock()
	if n == nil {
		return
	}

	title, body = truncateRunes(title, notifyBodyRunes), truncateRunes(body, notifyBodyRunes)
	go func() {
		if err := n.Notify(title, body); err != nil {
			// Debug, not warn: the one warning worth reading is the one the
			// notifier logs once when it knows it cannot work at all.
			a.log.Debug("notification not delivered", "error", err)
		}
	}()
}

// displayName is what a notification calls somebody. A name is server-supplied
// text, so it is trimmed and bounded like everything else that crosses into a
// system UI.
func displayName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Участник"
	}
	return truncateRunes(name, 64)
}

// truncateRunes cuts to n runes, marking the cut. Counted in runes, not bytes,
// so a Russian name is not cut in half mid-character.
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	count := 0
	for i := range s {
		if count == n {
			return strings.TrimRight(s[:i], " ") + "…"
		}
		count++
	}
	return s
}
