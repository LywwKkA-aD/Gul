package main

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// System notifications, and the honest degradation they need.
//
// VERIFIED, and the reason this is not in application.Options.Services: the
// Wails notifications service fails its startup on macOS when the process has
// no bundle identifier - which is every `wails3 dev` run and every bare
// binary - and a service whose startup returns an error takes the WHOLE
// application down with it (application.go: startupService's error aborts the
// run). So the service is constructed and started by hand here, its failure is
// recorded rather than propagated, and Gul keeps running with notifications
// switched off.
//
// The other half of honesty is saying so once, at a level a developer reading
// gul.log will see, and never again: a client that logged a warning per
// undelivered notification would bury the line that matters.

// notifyThreadID groups Gul's notifications so a platform that supports
// threading stacks them instead of listing twenty separate cards.
const notifyThreadID = "gul.channel"

// systemNotifier is core.Notifier over the Wails notifications service.
//
// The zero value is a notifier that does nothing, which is exactly what a
// machine without working notifications gets.
type systemNotifier struct {
	log *slog.Logger

	svc   *notifications.NotificationService
	ready atomic.Bool
	// seq makes each notification's identifier unique. Reusing one would ask
	// the platform to replace the previous notification rather than post a
	// new one.
	seq atomic.Uint64

	warnOnce sync.Once
	stopOnce sync.Once
}

func newSystemNotifier(log *slog.Logger) *systemNotifier {
	return &systemNotifier{log: log, svc: notifications.New()}
}

// start brings the platform notifier up and asks for permission where the
// platform has a concept of it.
//
// Call after the application is running: the Linux and Windows backends read
// application.Get().Config() during startup, and asking macOS for
// authorization puts a system dialog on screen. It blocks - macOS allows the
// user three minutes to answer - so it belongs on a goroutine of its own.
func (n *systemNotifier) start(ctx context.Context) {
	if err := n.svc.ServiceStartup(ctx, application.ServiceOptions{}); err != nil {
		// The expected case on an unpackaged macOS build, and a plausible one
		// on a Linux session with no notification daemon. Not fatal, and not
		// repeated.
		n.unavailable("system notifications are not available in this build", err)
		return
	}

	granted, err := n.svc.CheckNotificationAuthorization()
	if err != nil {
		n.unavailable("notification authorization could not be checked", err)
		_ = n.svc.ServiceShutdown()
		return
	}
	if !granted {
		granted, err = n.svc.RequestNotificationAuthorization()
		if err != nil {
			n.unavailable("notification authorization was not granted", err)
			_ = n.svc.ServiceShutdown()
			return
		}
	}
	if !granted {
		// The user said no. Their choice, and it stands until they change it
		// in the system settings.
		n.unavailable("system notifications are turned off for Gul", nil)
		_ = n.svc.ServiceShutdown()
		return
	}

	n.ready.Store(true)
	n.log.Info("system notifications ready")
}

// stop releases what the platform backend holds (a D-Bus connection on Linux,
// scheduled timers everywhere). Safe to call when start never succeeded.
func (n *systemNotifier) stop() {
	if !n.ready.Load() {
		return
	}
	n.stopOnce.Do(func() {
		n.ready.Store(false)
		if err := n.svc.ServiceShutdown(); err != nil {
			n.log.Debug("notification service shutdown", "error", err)
		}
	})
}

// Notify implements core.Notifier. On a machine where notifications do not
// work it does nothing and says so, which core treats as the courtesy it is.
func (n *systemNotifier) Notify(title, body string) error {
	if !n.ready.Load() {
		return nil
	}
	return n.svc.SendNotification(notifications.NotificationOptions{
		ID:       "gul-" + strconv.FormatUint(n.seq.Add(1), 36),
		Title:    title,
		Body:     body,
		ThreadID: notifyThreadID,
	})
}

// unavailable records, exactly once, why this machine will not be notifying.
// Warn rather than error: nothing is broken, a courtesy is simply off.
func (n *systemNotifier) unavailable(reason string, err error) {
	n.warnOnce.Do(func() {
		if err != nil {
			n.log.Warn(reason, "error", err)
			return
		}
		n.log.Warn(reason)
	})
}
