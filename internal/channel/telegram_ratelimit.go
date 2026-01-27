package channel

import (
	"sync"
	"time"
)

// chatRateLimiter throttles outbound messages per chat to respect Telegram API limits.
type chatRateLimiter struct {
	mu       sync.Mutex
	lastSent map[int64]time.Time
	minDelay time.Duration
}

func newChatRateLimiter() *chatRateLimiter {
	return &chatRateLimiter{
		lastSent: make(map[int64]time.Time),
		minDelay: time.Second,
	}
}

// wait blocks until it is safe to send to chatID.
func (r *chatRateLimiter) wait(chatID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if last, ok := r.lastSent[chatID]; ok {
		if elapsed := time.Since(last); elapsed < r.minDelay {
			time.Sleep(r.minDelay - elapsed)
		}
	}
	r.lastSent[chatID] = time.Now()
}

func (r *chatRateLimiter) cleanup() {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := time.Now().Add(-5 * time.Minute)
	for id, t := range r.lastSent {
		if t.Before(cutoff) {
			delete(r.lastSent, id)
		}
	}
}

// userRateLimiter enforces a per-user message rate using a sliding window.
type userRateLimiter struct {
	mu         sync.Mutex
	timestamps map[int64][]time.Time
	maxMsgs    int
	window     time.Duration
}

func newUserRateLimiter(maxMsgs, windowSec int) *userRateLimiter {
	if maxMsgs <= 0 {
		maxMsgs = 10
	}
	if windowSec <= 0 {
		windowSec = 60
	}
	return &userRateLimiter{
		timestamps: make(map[int64][]time.Time),
		maxMsgs:    maxMsgs,
		window:     time.Duration(windowSec) * time.Second,
	}
}

// allow returns true if userID is within the rate limit, recording the attempt.
func (r *userRateLimiter) allow(userID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-r.window)

	var recent []time.Time
	for _, t := range r.timestamps[userID] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}

	if len(recent) >= r.maxMsgs {
		r.timestamps[userID] = recent
		return false
	}

	r.timestamps[userID] = append(recent, now)
	return true
}

func (r *userRateLimiter) cleanup() {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := time.Now().Add(-r.window * 2)
	for uid, ts := range r.timestamps {
		var kept []time.Time
		for _, t := range ts {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			delete(r.timestamps, uid)
		} else {
			r.timestamps[uid] = kept
		}
	}
}
