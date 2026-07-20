package syncer

import (
	"context"
	"time"

	"github.com/erikvoit/dharana-cli/internal/output"
)

type WatchOptions struct {
	Interval   time.Duration
	MaxBackoff time.Duration
	Once       bool
}

type WatchRecord struct {
	SchemaVersion string       `json:"schema_version"`
	Type          string       `json:"record_type"`
	ObservedAt    string       `json:"observed_at"`
	Event         *EventRecord `json:"event,omitempty"`
	Sync          *PullResult  `json:"sync,omitempty"`
	Code          string       `json:"code,omitempty"`
	Message       string       `json:"message,omitempty"`
}

func (s *Service) Watch(ctx context.Context, opts WatchOptions, emit func(WatchRecord) error) error {
	if emit == nil {
		return output.NewError("WATCH_WRITER_REQUIRED", "Watch requires an event writer.")
	}
	if opts.Interval == 0 {
		opts.Interval = 30 * time.Second
	}
	if opts.Interval < time.Second || opts.Interval > 15*time.Minute {
		return output.NewError("WATCH_INTERVAL_INVALID", "Watch interval must be between 1 second and 15 minutes.")
	}
	if opts.MaxBackoff == 0 {
		opts.MaxBackoff = 2 * time.Minute
	}
	if opts.MaxBackoff < opts.Interval || opts.MaxBackoff > 15*time.Minute {
		return output.NewError("WATCH_BACKOFF_INVALID", "Maximum watch backoff must be at least the interval and no more than 15 minutes.")
	}
	backoff := opts.Interval
	for {
		result, err := s.Pull(ctx)
		if err != nil {
			code := "SYNC_PULL_FAILED"
			message := "Incremental synchronization failed."
			if appErr, ok := err.(*output.AppError); ok {
				code, message = appErr.Code, appErr.Message
			}
			if emitErr := emit(WatchRecord{SchemaVersion: SchemaVersion, Type: "sync.error", ObservedAt: s.now().UTC().Format(time.RFC3339), Code: code, Message: message}); emitErr != nil {
				return emitErr
			}
			if opts.Once {
				return err
			}
			backoff *= 2
			if backoff > opts.MaxBackoff {
				backoff = opts.MaxBackoff
			}
		} else {
			backoff = opts.Interval
			if result.Rebuilt {
				if err := emit(WatchRecord{SchemaVersion: SchemaVersion, Type: "sync.rebuilt", ObservedAt: s.now().UTC().Format(time.RFC3339), Sync: result}); err != nil {
					return err
				}
			}
			for i := range result.Events {
				event := result.Events[i]
				if err := emit(WatchRecord{SchemaVersion: SchemaVersion, Type: "event", ObservedAt: s.now().UTC().Format(time.RFC3339), Event: &event}); err != nil {
					return err
				}
			}
			if opts.Once {
				return nil
			}
		}
		timer := time.NewTimer(jittered(backoff, s.now()))
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			_ = emit(WatchRecord{SchemaVersion: SchemaVersion, Type: "watch.stopped", ObservedAt: s.now().UTC().Format(time.RFC3339), Message: "Watch stopped after safely committing the last processed cursor."})
			return nil
		case <-timer.C:
		}
	}
}

func jittered(base time.Duration, now time.Time) time.Duration {
	if base <= 0 {
		return base
	}
	window := base / 10
	if window == 0 {
		return base
	}
	offset := time.Duration(now.UnixNano()%int64(window*2+1)) - window
	return base + offset
}
