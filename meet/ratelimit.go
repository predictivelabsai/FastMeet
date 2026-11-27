package main

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ipLimiter applies a per-minute and a per-hour token bucket per client IP.
// Idle entries are dropped so memory stays bounded.
type ipLimiter struct {
	mu      sync.Mutex
	perMin  int
	perHour int
	m       map[string]*ipEntry
	last    time.Time
}

type ipEntry struct {
	min, hour *rate.Limiter
	seen      time.Time
}

func newIPLimiter(perMin, perHour int) *ipLimiter {
	return &ipLimiter{perMin: perMin, perHour: perHour, m: map[string]*ipEntry{}, last: time.Now()}
}

func (l *ipLimiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if now.Sub(l.last) > 10*time.Minute {
		for k, e := range l.m {
			if now.Sub(e.seen) > time.Hour {
				delete(l.m, k)
			}
		}
		l.last = now
	}
	e, ok := l.m[ip]
	if !ok {
		e = &ipEntry{
			min:  rate.NewLimiter(rate.Every(time.Minute/time.Duration(max(l.perMin, 1))), max(l.perMin, 1)),
			hour: rate.NewLimiter(rate.Every(time.Hour/time.Duration(max(l.perHour, 1))), max(l.perHour, 1)),
		}
		l.m[ip] = e
	}
	e.seen = now
	// Check both buckets without spending from one when the other refuses.
	if e.min.TokensAt(now) < 1 || e.hour.TokensAt(now) < 1 {
		return false
	}
	e.min.AllowN(now, 1)
	e.hour.AllowN(now, 1)
	return true
}
