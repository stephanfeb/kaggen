package channel

import (
	"sync"
	"time"
)

// phoneRateLimiter enforces a per-phone-number message rate using a sliding window.
type phoneRateLimiter struct {
	mu         sync.Mutex
	timestamps map[string][]time.Time
	maxMsgs    int
	window     time.Duration
}

func newPhoneRateLimiter(maxMsgs, windowSec int) *phoneRateLimiter {
	if maxMsgs <= 0 {
		maxMsgs = 10
	}
	if windowSec <= 0 {
		windowSec = 60
	}
	return &phoneRateLimiter{
		timestamps: make(map[string][]time.Time),
		maxMsgs:    maxMsgs,
		window:     time.Duration(windowSec) * time.Second,
	}
}

// allow returns true if phone is within the rate limit, recording the attempt.
func (r *phoneRateLimiter) allow(phone string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-r.window)

	var recent []time.Time
	for _, t := range r.timestamps[phone] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}

	if len(recent) >= r.maxMsgs {
		r.timestamps[phone] = recent
		return false
	}

	r.timestamps[phone] = append(recent, now)
	return true
}

func (r *phoneRateLimiter) cleanup() {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := time.Now().Add(-r.window * 2)
	for phone, ts := range r.timestamps {
		var kept []time.Time
		for _, t := range ts {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			delete(r.timestamps, phone)
		} else {
			r.timestamps[phone] = kept
		}
	}
}

// chatRateLimiterWA throttles outbound messages per chat to respect WhatsApp API limits.
type chatRateLimiterWA struct {
	mu       sync.Mutex
	lastSent map[string]time.Time
	minDelay time.Duration
}

func newChatRateLimiterWA() *chatRateLimiterWA {
	return &chatRateLimiterWA{
		lastSent: make(map[string]time.Time),
		minDelay: time.Second,
	}
}

// wait blocks until it is safe to send to chatJID.
func (r *chatRateLimiterWA) wait(chatJID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if last, ok := r.lastSent[chatJID]; ok {
		if elapsed := time.Since(last); elapsed < r.minDelay {
			time.Sleep(r.minDelay - elapsed)
		}
	}
	r.lastSent[chatJID] = time.Now()
}

func (r *chatRateLimiterWA) cleanup() {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := time.Now().Add(-5 * time.Minute)
	for jid, t := range r.lastSent {
		if t.Before(cutoff) {
			delete(r.lastSent, jid)
		}
	}
}
