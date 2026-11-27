package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"strconv"
	"time"

	"github.com/pion/logging"
	"github.com/pion/turn/v4"
)

// iceServer mirrors the browser's RTCIceServer dictionary.
type iceServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// ICE hands out STUN servers plus short-lived TURN credentials (TURN REST API
// style: username "<expiry>", password base64(HMAC-SHA1(secret, username))).
type ICE struct {
	stun     []string
	turnURLs []string
	secret   string
	ttl      time.Duration
}

func (i *ICE) turnEnabled() bool { return len(i.turnURLs) > 0 && i.secret != "" }

// Servers returns the iceServers list for one client.
func (i *ICE) Servers() []iceServer {
	out := []iceServer{}
	if len(i.stun) > 0 {
		out = append(out, iceServer{URLs: i.stun})
	}
	if i.turnEnabled() {
		user, pass, err := turn.GenerateLongTermCredentials(i.secret, i.ttl)
		if err == nil {
			out = append(out, iceServer{URLs: i.turnURLs, Username: user, Credential: pass})
		}
	}
	return out
}

func randomSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// setupICE builds the ICE config and, when TURN_EMBEDDED=1, starts a pion TURN
// server on UDP TURN_PORT relaying through TURN_RELAY_MIN..MAX.
func setupICE(cfg Config) (*ICE, func(), error) {
	ice := &ICE{stun: cfg.STUNURLs, turnURLs: cfg.TURNURLs, secret: cfg.TURNSecret, ttl: 6 * time.Hour}
	if !cfg.TURNEmbedded {
		return ice, func() {}, nil
	}
	if cfg.TURNPublicIP == "" || net.ParseIP(cfg.TURNPublicIP) == nil {
		return nil, nil, fmt.Errorf("TURN_EMBEDDED=1 needs a valid TURN_PUBLIC_IP")
	}
	if ice.secret == "" {
		ice.secret = randomSecret() // credentials are only ever minted by this process
	}
	addr := "0.0.0.0:" + strconv.Itoa(cfg.TURNPort)
	udp, err := net.ListenPacket("udp4", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("turn listen %s: %w", addr, err)
	}
	lf := logging.NewDefaultLoggerFactory()
	lf.DefaultLogLevel = logging.LogLevelWarn
	s, err := turn.NewServer(turn.ServerConfig{
		Realm:         cfg.TURNRealm,
		LoggerFactory: lf,
		AuthHandler:   turn.NewLongTermAuthHandler(ice.secret, lf.NewLogger("auth")),
		PacketConnConfigs: []turn.PacketConnConfig{{
			PacketConn: udp,
			RelayAddressGenerator: &turn.RelayAddressGeneratorPortRange{
				RelayAddress: net.ParseIP(cfg.TURNPublicIP),
				Address:      "0.0.0.0",
				MinPort:      uint16(cfg.TURNRelayMin),
				MaxPort:      uint16(cfg.TURNRelayMax),
			},
		}},
	})
	if err != nil {
		return nil, nil, err
	}
	if len(ice.turnURLs) == 0 {
		hp := net.JoinHostPort(cfg.TURNPublicIP, strconv.Itoa(cfg.TURNPort))
		ice.turnURLs = []string{"turn:" + hp + "?transport=udp"}
	}
	log.Printf("embedded TURN on udp %s, relay %d-%d", addr, cfg.TURNRelayMin, cfg.TURNRelayMax)
	return ice, func() { _ = s.Close() }, nil
}
