package engine

// Re-exports for desktop notifications, so the UI packages reach
// internal/notify only through engine.

import "github.com/TTox-Tech/chitragit/internal/notify"

type (
	Notification = notify.Message
	Notifier     = notify.Notifier
	Sender       = notify.Sender
)

const (
	NotifyRescued     = notify.Rescued
	NotifyRunFinished = notify.RunFinished
)

func NotifyGroup(n int) string { return notify.Group(n) }

func NativeNotifier() notify.Sender { return notify.Native() }
