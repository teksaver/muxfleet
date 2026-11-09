package ports

import (
	"context"
	"time"
)

// Notifier defines the notifications port (desktop reminder, push via moshi-hook) (AD-13, AD-21, R12).
// Draft port (see package doc): adjust it to the ADs that bind it before implementing it.
type Notifier interface {
	SendDesktopNotification(ctx context.Context, title string, body string) error
	ScheduleLocalReminder(ctx context.Context, serverName string, delay time.Duration) error
	CancelLocalReminder(ctx context.Context, serverName string) error
	SendPushNotification(ctx context.Context, title string, body string, link string) error
}
