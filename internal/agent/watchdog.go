package agent

import (
	"context"
	"log/slog"
	"time"
)

// AlertFunc is called when a task has been running longer than the threshold.
type AlertFunc func(task *TaskState, duration time.Duration)

// Watchdog periodically scans for stale running tasks and fires alerts.
type Watchdog struct {
	store     *InFlightStore
	threshold time.Duration
	alertFn   AlertFunc
	logger    *slog.Logger
}

// NewWatchdog creates a new task watchdog.
func NewWatchdog(store *InFlightStore, threshold time.Duration, alertFn AlertFunc, logger *slog.Logger) *Watchdog {
	return &Watchdog{
		store:     store,
		threshold: threshold,
		alertFn:   alertFn,
		logger:    logger,
	}
}

// Start runs the watchdog loop. It checks every 60 seconds for stale tasks.
// Each task is only alerted once. Blocks until ctx is cancelled.
func (w *Watchdog) Start(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	alerted := make(map[string]bool)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, t := range w.store.List(TaskRunning) {
				dur := time.Since(t.StartedAt)
				if dur > w.threshold && !alerted[t.ID] {
					alerted[t.ID] = true
					w.logger.Warn("stale task detected",
						"task_id", t.ID,
						"agent", t.AgentName,
						"duration", dur.Round(time.Second),
						"threshold", w.threshold)
					if w.alertFn != nil {
						w.alertFn(t, dur)
					}
				}
			}
			// Clean up alerts for tasks that are no longer running.
			for id := range alerted {
				if t, ok := w.store.Get(id); !ok || t.Status != TaskRunning {
					delete(alerted, id)
				}
			}
		}
	}
}
