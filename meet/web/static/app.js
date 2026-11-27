// FastMeet room client: peer-to-peer WebRTC mesh with websocket signaling.
(function () {
  "use strict";
  var body = document.body;
  var ROOM = body.dataset.room, URL_ = body.dataset.url;
  var $ = function (id) { return document.getElementById(id); };

  // Exposed for automated checks: ICE state per remote peer.
  var FM = window.__fm = { me: null, ice: {}, peers: 0, joined: false };

  var local = null, ws = null, iceServers = [], me = null;
  var wantAudio = true, wantVideo = true, leaving = false;
  var peers = new Map(); // id -> {pc, polite, makingOffer, ignoreOffer, tile, name}
  var names = new Map();
  FM._peers = peers;

  try { $("name").value = localStorage.getItem("fm-name") || ""; } catch (e) {}

  function setToggle(btn, on, onText, offText) {
    btn.classList.toggle("on", on); btn.classList.toggle("off", !on);
    btn.setAttribute("aria-pressed", String(on));
    btn.textContent = on ? onText : offText;
  }

  async function getMedia() {
    var tries = [{ audio: true, video: { width: { ideal: 1280 }, height: { ideal: 720 }, facingMode: "user" } },
                 { audio: true, video: false }];
    for (var i = 0; i < tries.length; i++) {
      try { return await navigator.mediaDevices.getUserMedia(tries[i]); } catch (e) { FM.mediaError = String(e && e.name); }
    }
    return null;
  }

  async function startPreview() {
    if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
      $("preview-note").textContent = "This browser can't use a camera here. Open the link in Chrome, Safari or Firefox.";
      return;
    }
    local = await getMedia();
    if (local) {
      $("preview").srcObject = local;
      $("preview-note").hidden = !!local.getVideoTracks().length;
      if (!local.getVideoTracks().length) { $("preview-note").textContent = "No camera found: you'll join with audio only."; $("preview-note").hidden = false; }
    } else {
      $("preview-note").textContent = "Camera and microphone are blocked. You can still join to see and hear the other person; allow access in your browser settings to be seen.";
    }
  }

  function applyTracks() {
    if (!local) return;
    local.getAudioTracks().forEach(function (t) { t.enabled = wantAudio; });
    local.getVideoTracks().forEach(function (t) { t.enabled = wantVideo; });
  }

  function copyLink(btn) {
    var done = function () { var t = btn.textContent; btn.textContent = "Copied"; setTimeout(function () { btn.textContent = t; }, 1500); };
    if (navigator.share && /Mobi|Android/i.test(navigator.userAgent)) {
      navigator.share({ title: "FastMeet", text: "Join my video call", url: URL_ }).catch(function () {});
      return;
    }
    if (navigator.clipboard) navigator.clipboard.writeText(URL_).then(done, function () { $("link").select(); });
    else { $("link").select(); document.execCommand("copy"); done(); }
  }

  // ---- tiles ----
  function makeTile(id, name, isLocal) {
    var tile = document.createElement("div");
    tile.className = "tile" + (isLocal ? " local" : "");
    tile.id = "tile-" + id;
    var v = document.createElement("video");
    v.autoplay = true; v.playsInline = true; if (isLocal) v.muted = true;
    var label = document.createElement("div"); label.className = "label";
    var n = document.createElement("span"); n.className = "nm"; n.textContent = isLocal ? name + " (you)" : name;
    var st = document.createElement("span"); st.className = "st";
    label.appendChild(n); label.appendChild(st);
    var ph = document.createElement("div"); ph.className = "ph"; ph.textContent = (name || "?").trim().charAt(0).toUpperCase();
    tile.appendChild(ph); tile.appendChild(v); tile.appendChild(label);
    $("grid").appendChild(tile);
    layout();
    return tile;
  }
  function layout() {
    var n = $("grid").children.length;
    $("grid").dataset.n = String(n);
    var others = peers.size;
    $("status").textContent = others ? "" : "Waiting for the other person to join. Send them the invite link.";
    $("status").hidden = !!others;
    FM.peers = others;
  }
  function tileState(id, text, cls) {
    var t = $("tile-" + id); if (!t) return;
    var st = t.querySelector(".st"); st.textContent = text || ""; t.dataset.state = cls || "";
  }

  // ---- signaling ----
  function send(o) { if (ws && ws.readyState === 1) ws.send(JSON.stringify(o)); }

  function connect() {
    leaving = false;
    var proto = location.protocol === "https:" ? "wss://" : "ws://";
    ws = new WebSocket(proto + location.host + "/ws/" + ROOM);
    ws.onopen = function () {
      send({ type: "join", name: $("name").value || "Guest", audio: wantAudio, video: wantVideo && !!(local && local.getVideoTracks().length) });
    };
    ws.onmessage = function (ev) {
      var m; try { m = JSON.parse(ev.data); } catch (e) { return; }
      onMessage(m);
    };
    ws.onclose = function () {
      if (leaving) return;
      peers.forEach(function (p, id) { closePeer(id); });
      $("status").hidden = false; $("status").textContent = "Connection lost. Reconnecting…";
      setTimeout(function () { if (!leaving) connect(); }, 2000);
    };
  }

  function onMessage(m) {
    switch (m.type) {
      case "welcome":
        me = FM.me = m.id; iceServers = m.ice || []; FM.joined = true;
        (m.peers || []).forEach(function (p) { names.set(p.id, p.name); getPeer(p.id, false); });
        layout();
        break;
      case "peer-joined":
        names.set(m.id, m.name || "Guest");
        chatLine(null, (m.name || "Guest") + " joined");
        break; // they will send us an offer; we are the polite side
      case "peer-left":
        chatLine(null, (names.get(m.id) || "Someone") + " left");
        closePeer(m.id);
        break;
      case "signal":
        onSignal(m.from, m.data);
        break;
      case "state":
        var t = $("tile-" + m.from);
        if (t) { if (m.audio !== undefined) t.classList.toggle("muted", !m.audio); if (m.video !== undefined) t.classList.toggle("novideo", !m.video); }
        break;
      case "chat":
        chatLine(m.from === me ? "You" : m.name, m.text);
        if ($("chat").hidden && m.from !== me) $("chatbtn").classList.add("unread");
        break;
      case "full":
        leaving = true; showEnded("This room is full", "It already has " + (m.max || 2) + " people in it. Ask for a new link, or start your own room.");
        break;
    }
  }

  // Perfect negotiation: the newcomer offers (impolite); people already in the room answer (polite).
  function getPeer(id, polite) {
    var p = peers.get(id);
    if (p) return p;
    var pc = new RTCPeerConnection({ iceServers: iceServers });
    p = { pc: pc, polite: polite, makingOffer: false, ignoreOffer: false };
    peers.set(id, p);
    p.tile = makeTile(id, names.get(id) || "Guest", false);
    tileState(id, "connecting…", "connecting");
    var kinds = { audio: false, video: false };
    if (local) local.getTracks().forEach(function (t) { pc.addTrack(t, local); kinds[t.kind] = true; });
    ["audio", "video"].forEach(function (k) { if (!kinds[k]) pc.addTransceiver(k, { direction: "recvonly" }); });

    pc.ontrack = function (ev) {
      var v = p.tile.querySelector("video");
      if (v.srcObject !== ev.streams[0]) { v.srcObject = ev.streams[0] || new MediaStream([ev.track]); }
      p.tile.classList.add("has-video");
      var pl = v.play(); if (pl && pl.catch) pl.catch(function () {});
    };
    pc.onicecandidate = function (ev) { if (ev.candidate) send({ type: "signal", to: id, data: { candidate: ev.candidate } }); };
    pc.oniceconnectionstatechange = function () {
      var s = pc.iceConnectionState; FM.ice[id] = s;
      if (s === "connected" || s === "completed") tileState(id, "", "ok");
      else if (s === "disconnected") tileState(id, "reconnecting…", "connecting");
      else if (s === "failed") { tileState(id, "connection failed, retrying…", "bad"); if (!p.polite) pc.restartIce(); }
      else if (s === "checking") tileState(id, "connecting…", "connecting");
    };
    pc.onnegotiationneeded = async function () {
      try {
        p.makingOffer = true;
        await pc.setLocalDescription();
        send({ type: "signal", to: id, data: { description: pc.localDescription } });
      } catch (e) { console.warn(e); } finally { p.makingOffer = false; }
    };
    return p;
  }

  async function onSignal(from, data) {
    if (!data) return;
    var p = getPeer(from, true);
    var pc = p.pc;
    try {
      if (data.description) {
        var d = data.description;
        var collision = d.type === "offer" && (p.makingOffer || pc.signalingState !== "stable");
        p.ignoreOffer = !p.polite && collision;
        if (p.ignoreOffer) return;
        await pc.setRemoteDescription(d);
        if (d.type === "offer") {
          await pc.setLocalDescription();
          send({ type: "signal", to: from, data: { description: pc.localDescription } });
        }
      } else if (data.candidate) {
        try { await pc.addIceCandidate(data.candidate); } catch (e) { if (!p.ignoreOffer) console.warn(e); }
      }
    } catch (e) { console.warn(e); }
  }

  function closePeer(id) {
    var p = peers.get(id); if (!p) return;
    try { p.pc.close(); } catch (e) {}
    if (p.tile) p.tile.remove();
    peers.delete(id); delete FM.ice[id];
    layout();
  }

  // ---- chat ----
  function chatLine(who, text) {
    var d = document.createElement("div");
    d.className = who ? "msg" : "msg sys";
    if (who) { var b = document.createElement("b"); b.textContent = who + ": "; d.appendChild(b); }
    d.appendChild(document.createTextNode(text));
    $("chatlog").appendChild(d); $("chatlog").scrollTop = 1e9;
  }

  function showEnded(title, text) {
    $("call").hidden = true; $("lobby").hidden = true; $("ended").hidden = false;
    $("ended-title").textContent = title; $("ended-text").textContent = text || "";
  }

  function leave() {
    leaving = true;
    try { ws && ws.close(); } catch (e) {}
    peers.forEach(function (p, id) { closePeer(id); });
    var lt = $("grid").querySelector(".tile.local"); if (lt) lt.remove();
    showEnded("You left the call", "Your camera and microphone are off.");
    if (local) { local.getTracks().forEach(function (t) { t.stop(); }); local = null; }
    FM.joined = false;
  }

  async function join() {
    try { localStorage.setItem("fm-name", $("name").value.trim()); } catch (e) {}
    if (!local) await startPreview();
    applyTracks();
    $("lobby").hidden = true; $("ended").hidden = true; $("call").hidden = false;
    setToggle($("mic"), wantAudio, "Mic", "Mic off");
    setToggle($("cam"), wantVideo, "Camera", "Camera off");
    var lt = makeTile("me", $("name").value.trim() || "You", true);
    if (local) { lt.querySelector("video").srcObject = local; if (local.getVideoTracks().length) lt.classList.add("has-video"); }
    lt.classList.toggle("novideo", !wantVideo);
    connect();
  }

  // ---- wiring ----
  $("pre-mic").onclick = function () { wantAudio = !wantAudio; applyTracks(); setToggle(this, wantAudio, "Mic on", "Mic off"); };
  $("pre-cam").onclick = function () { wantVideo = !wantVideo; applyTracks(); setToggle(this, wantVideo, "Camera on", "Camera off"); };
  $("join").onclick = join;
  $("name").addEventListener("keydown", function (e) { if (e.key === "Enter") join(); });
  $("copy").onclick = function () { copyLink(this); };
  $("copy2").onclick = function () { copyLink(this); };
  $("mic").onclick = function () { wantAudio = !wantAudio; applyTracks(); setToggle(this, wantAudio, "Mic", "Mic off"); send({ type: "state", audio: wantAudio }); };
  $("cam").onclick = function () {
    wantVideo = !wantVideo; applyTracks(); setToggle(this, wantVideo, "Camera", "Camera off");
    var lt = $("tile-me"); if (lt) lt.classList.toggle("novideo", !wantVideo);
    send({ type: "state", video: wantVideo });
  };
  $("chatbtn").onclick = function () { $("chat").hidden = !$("chat").hidden; this.classList.remove("unread"); if (!$("chat").hidden) $("chatinput").focus(); };
  $("chatform").onsubmit = function (e) { e.preventDefault(); var v = $("chatinput").value.trim(); if (v) send({ type: "chat", text: v }); $("chatinput").value = ""; };
  $("leave").onclick = leave;
  $("rejoin").onclick = function () { join(); };
  window.addEventListener("pagehide", function () { leaving = true; try { ws && ws.close(); } catch (e) {} });

  startPreview();
})();
