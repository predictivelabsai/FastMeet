package main

import (
	"os"
	"strconv"
	"strings"
)

// Config is read from the environment; every field has a safe default.
type Config struct {
	Addr         string // listen address, default :8080
	PublicURL    string // e.g. https://meet.socialagent.fun, used for share links
	HomeURL      string // "Powered by" link target
	MaxPeers     int    // participants per room
	TrustProxy   bool   // trust X-Real-Ip / X-Forwarded-For from the reverse proxy
	CreatePerMin int    // room creations per IP per minute
	CreatePerHr  int    // room creations per IP per hour
	STUNURLs     []string

	// External TURN (e.g. coturn with use-auth-secret).
	TURNURLs   []string
	TURNSecret string

	// Embedded TURN (pion/turn) on this host.
	TURNEmbedded bool
	TURNPublicIP string
	TURNPort     int
	TURNRealm    string
	TURNRelayMin int
	TURNRelayMax int
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if n, err := strconv.Atoi(env(k, "")); err == nil {
		return n
	}
	return def
}

func envList(k, def string) []string {
	var out []string
	for _, s := range strings.Split(env(k, def), ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func loadConfig() Config {
	return Config{
		Addr:         env("ADDR", ":8080"),
		PublicURL:    strings.TrimRight(env("PUBLIC_URL", ""), "/"),
		HomeURL:      env("HOME_URL", "https://socialagent.fun"),
		MaxPeers:     envInt("MAX_PEERS", 4),
		TrustProxy:   env("TRUST_PROXY", "1") == "1",
		CreatePerMin: envInt("CREATE_PER_MIN", 6),
		CreatePerHr:  envInt("CREATE_PER_HOUR", 30),
		STUNURLs:     envList("STUN_URLS", "stun:stun.l.google.com:19302,stun:stun.cloudflare.com:3478"),
		TURNURLs:     envList("TURN_URLS", ""),
		TURNSecret:   env("TURN_SECRET", ""),
		TURNEmbedded: env("TURN_EMBEDDED", "0") == "1",
		TURNPublicIP: env("TURN_PUBLIC_IP", ""),
		TURNPort:     envInt("TURN_PORT", 3478),
		TURNRealm:    env("TURN_REALM", "meet.socialagent.fun"),
		TURNRelayMin: envInt("TURN_RELAY_MIN", 49160),
		TURNRelayMax: envInt("TURN_RELAY_MAX", 49200),
	}
}
