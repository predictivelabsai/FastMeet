package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"golang.org/x/time/rate"
)

// Signaling protocol (JSON text frames). The server only relays: SDP and ICE
// candidates go peer to peer via "signal", and media never touches the server.
//
//	client -> server: {"type":"join","name":"Ana"}
//	server -> client: {"type":"welcome","id":"<you>","peers":[{"id","name"}],"ice":[...]}
//	server -> others: {"type":"peer-joined","id","name"} / {"type":"peer-left","id"}
//	client -> server: {"type":"signal","to":"<peer>","data":{...}}  (forwarded with "from")
//	client -> server: {"type":"chat","text":"hi"}                  (broadcast, not stored)
//	client -> server: {"type":"state","audio":true,"video":false}   (broadcast)
//	server -> client: {"type":"full"} when the room already has MaxPeers people.
type msg struct {
	Type  string          `json:"type"`
	ID    string          `json:"id,omitempty"`
	From  string          `json:"from,omitempty"`
	To    string          `json:"to,omitempty"`
	Name  string          `json:"name,omitempty"`
	Text  string          `json:"text,omitempty"`
	Audio *bool           `json:"audio,omitempty"`
	Video *bool           `json:"video,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
	Peers []peerInfo      `json:"peers,omitempty"`
	ICE   []iceServer     `json:"ice,omitempty"`
	Max   int             `json:"max,omitempty"`
}

type peerInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Audio bool   `json:"audio"`
	Video bool   `json:"video"`
}

type peer struct {
	info peerInfo
	send chan []byte
}

type room struct {
	peers map[string]*peer
}

type hub struct {
	mu    sync.Mutex
	max   int
	rooms map[string]*room
}

func newHub(max int) *hub { return &hub{max: max, rooms: map[string]*room{}} }

func peerID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func cleanName(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if !utf8.ValidString(s) {
		s = ""
	}
	if r := []rune(s); len(r) > 40 {
		s = string(r[:40])
	}
	if s == "" {
		s = "Guest"
	}
	return s
}

// join adds p to room id and returns the peers already there, or ok=false if full.
func (h *hub) join(id string, p *peer) (others []peerInfo, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.rooms[id]
	if r == nil {
		r = &room{peers: map[string]*peer{}}
		h.rooms[id] = r
	}
	if len(r.peers) >= h.max {
		return nil, false
	}
	for _, o := range r.peers {
		others = append(others, o.info)
	}
	r.peers[p.info.ID] = p
	return others, true
}

func (h *hub) leave(id string, p *peer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r := h.rooms[id]; r != nil {
		delete(r.peers, p.info.ID)
		if len(r.peers) == 0 {
			delete(h.rooms, id) // rooms live only while someone is in them
		}
	}
}

// deliver sends b to one peer (to != "") or to everyone except from.
func (h *hub) deliver(id, from, to string, b []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.rooms[id]
	if r == nil {
		return
	}
	for pid, p := range r.peers {
		if pid == from || (to != "" && pid != to) {
			continue
		}
		select {
		case p.send <- b:
		default: // slow consumer: drop rather than block the room
		}
	}
}

func (h *hub) setState(id, pid string, audio, video *bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r := h.rooms[id]; r != nil {
		if p := r.peers[pid]; p != nil {
			if audio != nil {
				p.info.Audio = *audio
			}
			if video != nil {
				p.info.Video = *video
			}
		}
	}
}

func (h *hub) count(id string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r := h.rooms[id]; r != nil {
		return len(r.peers)
	}
	return 0
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func (s *server) ws(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("room")
	if !validRoomID(id) {
		http.NotFound(w, r)
		return
	}
	if !s.connects.Allow(s.clientIP(r)) {
		http.Error(w, "too many connections", http.StatusTooManyRequests)
		return
	}
	c, err := websocket.Accept(w, r, nil) // default: Origin must match Host
	if err != nil {
		return
	}
	c.SetReadLimit(64 << 10)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer c.CloseNow()

	// First frame must be a join, within 15s.
	jctx, jcancel := context.WithTimeout(ctx, 15*time.Second)
	_, data, err := c.Read(jctx)
	jcancel()
	var first msg
	if err != nil || json.Unmarshal(data, &first) != nil || first.Type != "join" {
		c.Close(websocket.StatusPolicyViolation, "expected join")
		return
	}
	p := &peer{
		info: peerInfo{ID: peerID(), Name: cleanName(first.Name), Audio: first.Audio == nil || *first.Audio, Video: first.Video == nil || *first.Video},
		send: make(chan []byte, 64),
	}
	others, ok := s.hub.join(id, p)
	if !ok {
		_ = c.Write(ctx, websocket.MessageText, mustJSON(msg{Type: "full", Max: s.hub.max}))
		c.Close(websocket.StatusTryAgainLater, "room full")
		return
	}
	defer func() {
		s.hub.leave(id, p)
		s.hub.deliver(id, p.info.ID, "", mustJSON(msg{Type: "peer-left", ID: p.info.ID}))
	}()

	if others == nil {
		others = []peerInfo{}
	}
	p.send <- mustJSON(msg{Type: "welcome", ID: p.info.ID, Peers: others, ICE: s.ice.Servers(), Max: s.hub.max})
	s.hub.deliver(id, p.info.ID, "", mustJSON(msg{Type: "peer-joined", ID: p.info.ID, Name: p.info.Name, Audio: &p.info.Audio, Video: &p.info.Video}))

	// Writer: queued frames plus a keepalive ping so proxies keep the socket open.
	go func() {
		t := time.NewTicker(25 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case b := <-p.send:
				wctx, wc := context.WithTimeout(ctx, 10*time.Second)
				err := c.Write(wctx, websocket.MessageText, b)
				wc()
				if err != nil {
					cancel()
					return
				}
			case <-t.C:
				pctx, pc := context.WithTimeout(ctx, 10*time.Second)
				err := c.Ping(pctx)
				pc()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	lim := rate.NewLimiter(50, 200) // ICE trickle is bursty; this still stops floods
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if !lim.Allow() {
			c.Close(websocket.StatusPolicyViolation, "slow down")
			return
		}
		var m msg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.Type {
		case "signal":
			if m.To == "" || len(m.Data) == 0 {
				continue
			}
			s.hub.deliver(id, p.info.ID, m.To, mustJSON(msg{Type: "signal", From: p.info.ID, Data: m.Data}))
		case "chat":
			text := strings.TrimSpace(m.Text)
			if text == "" || !utf8.ValidString(text) {
				continue
			}
			if r := []rune(text); len(r) > 1000 {
				text = string(r[:1000])
			}
			s.hub.deliver(id, "", "", mustJSON(msg{Type: "chat", From: p.info.ID, Name: p.info.Name, Text: text}))
		case "state":
			s.hub.setState(id, p.info.ID, m.Audio, m.Video)
			s.hub.deliver(id, p.info.ID, "", mustJSON(msg{Type: "state", From: p.info.ID, Audio: m.Audio, Video: m.Video}))
		}
	}
}
