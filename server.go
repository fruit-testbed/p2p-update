// Copyright 2018 University of Glasgow.
// Use of this source code is governed by an Apache
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/valyala/fasthttp"

	"github.com/gortc/stun"
	"github.com/pkg/errors"
)

// ServerConfig contains the server configuration parameters.
type ServerConfig struct {
	Address              string `json:"address"`
	SessionAdvertiseTime int    `json:"session-advertise-time"` // in seconds
	Database             string `json:"database"`
	SnapshotTime         int    `json:"snapshot-time"` // in seconds
	PublicKey            Key    `json:"public-key"`
	StunPassword         string `json:"stun-password"`
}

// DefaultServerConfig returns default server configurations.
func DefaultServerConfig() *ServerConfig {
	cfg := &ServerConfig{
		Address:              "",
		SessionAdvertiseTime: 60,
		Database:             "server.db",
		SnapshotTime:         5,
		PublicKey: Key{
			Filename: "key.pub",
		},
		StunPassword: defaultStunPassword,
	}
	return cfg
}

// Server is a STUN server implementation for multicast messaging system
type Server struct {
	sync.RWMutex
	Addr  *net.UDPAddr
	ID    PeerID
	peers SessionTable
	cfg   *ServerConfig

	udpConn   *net.UDPConn
	publicKey *rsa.PublicKey

	updates      map[string]*Notification
	lastModified time.Time
	lastSaved    time.Time
}

// NewServer returns an instance of Server
func NewServer(cfg ServerConfig) (*Server, error) {
	var (
		id   *PeerID
		addr *net.UDPAddr
		pub  *rsa.PublicKey
		err  error
	)

	j, _ := json.Marshal(cfg)
	log.Printf("creating server with config: %s", string(j))

	if addr, err = net.ResolveUDPAddr("udp", cfg.Address); err != nil {
		return nil, errors.Wrapf(err, "failed resolving address %s", cfg.Address)
	}
	if id, err = LocalPeerID(); err != nil {
		return nil, errors.Wrap(err, "Cannot get local ID")
	}

	// load public key file
	if pub, err = LoadPublicKey(cfg.PublicKey.Filename); err != nil {
		return nil, fmt.Errorf("ERROR: failed loading public key file '%s: %v",
			cfg.PublicKey.Filename, err)
	}

	s := &Server{
		Addr:      addr,
		ID:        *id,
		peers:     make(SessionTable),
		cfg:       &cfg,
		publicKey: pub,
	}
	if err = s.loadUpdates(); err != nil {
		return nil, errors.Wrap(err, "failed loading update database")
	}

	j, _ = json.Marshal(s.cfg)
	log.Printf("created server with config: %s", string(j))

	return s, nil
}

func (s *Server) run(wg *sync.WaitGroup) {
	defer wg.Done()

	go s.serveTCP()
	s.serveUDP()
}

func (s *Server) serveTCP() {
	log.Printf("Serving TCP (HTTP) at %s", s.Addr.String())
	if err := fasthttp.ListenAndServe(s.Addr.String(), s.serveHTTPRequest); err != nil {
		log.Fatalf("failed serving TCP at %s - %v", s.Addr.String(), err)
	}
}

func (s *Server) serveHTTPRequest(ctx *fasthttp.RequestCtx) {
	switch {
	case bytes.Compare(ctx.Method(), strGET) == 0:
		s.serveGetRequest(ctx)
	case bytes.Compare(ctx.Method(), strPOST) == 0:
		s.servePostRequest(ctx)
	default:
		ctx.SetStatusCode(400)
	}
}

func (s *Server) serveGetRequest(ctx *fasthttp.RequestCtx) {
	s.RLock()
	doJSONWrite(ctx, 200, s.updates)
	s.RUnlock()
}

func (s *Server) servePostRequest(ctx *fasthttp.RequestCtx) {
	var (
		m   Notification
		err error
	)
	if err = json.Unmarshal(ctx.PostBody(), &m); err == nil {
		if err = m.Verify(s.publicKey); err == nil {
			s.Lock()
			if old, ok := s.updates[m.UUID]; !ok || old.Version < m.Version {
				s.updates[m.UUID] = &m
				s.lastModified = time.Now()
			}
			s.Unlock()
		}
	}
	if err != nil {
		log.Println(string(ctx.PostBody()))
		ctx.SetStatusCode(403)
	}
}

func (s *Server) serveUDP() {
	conn, err := net.ListenUDP("udp", s.Addr)
	if err != nil {
		log.Printf("failed listening UDP at %s - %v", s.Addr.String(), err)
		return
	}
	s.udpConn = conn

	ExecEvery(time.Duration(s.cfg.SessionAdvertiseTime)*time.Second, s.advertiseSessionTable)
	ExecEvery(time.Duration(s.cfg.SnapshotTime)*time.Second, s.saveUpdates)

	log.Printf("Serving UDP (STUN) at %s with id:%s", s.Addr.String(), s.ID.String())

	jobs := make(chan stunRequestJob, 100)
	for w := 1; w <= 3; w++ {
		go s.udpWorker(w, jobs)
	}

	buf := make([]byte, 2048)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			log.Printf("ERROR: ReadFrom %v - %v", addr, err)
			continue
		}

		msg := buf[:n]
		if !stun.IsMessage(msg) {
			log.Printf("message sent by %s is not STUN", addr)
			continue
		}

		req := stunMessagePool.Get().(*stun.Message)
		req.Reset()
		if _, err := req.Write(msg); err != nil {
			log.Printf("sender %s: failed to read stun message", addr)
			stunMessagePool.Put(req)
			continue
		}

		jobs <- stunRequestJob{
			conn:     conn,
			addr:     addr,
			request:  req,
			response: stunMessagePool.Get().(*stun.Message),
		}
	}
	//close(jobs)
}

type stunRequestJob struct {
	conn     net.PacketConn
	addr     net.Addr
	request  *stun.Message
	response *stun.Message
}

func (s *Server) udpWorker(id int, jobs <-chan stunRequestJob) {
	for j := range jobs {
		log.Printf("worker %d - processMessage from %s", id, j.addr)
		if err := s.processMessage(j.conn, j.addr, j.request, j.response); err != nil {
			log.Printf("worker %d - ERROR: processMessage from %s: %v", id, j.addr, err)
		}
		stunMessagePool.Put(j.request)
		stunMessagePool.Put(j.response)
	}
}

func (s *Server) processMessage(c net.PacketConn, addr net.Addr, req, res *stun.Message) error {
	if err := validateMessage(req, nil, s.cfg.StunPassword); err != nil {
		return errors.Wrap(err, "Invalid message")
	}
	if req.Type == stun.BindingRequest {
		return s.registerPeer(c, addr, req, res)
	}
	return fmt.Errorf("message type is not STUN binding")
}

func (s *Server) registerPeer(conn net.PacketConn, addr net.Addr, req, res *stun.Message) error {
	// Extract Peer's ID, IP, and port from the message, then register it
	var (
		pid          = new(PeerID)
		xorAddr      stun.XORMappedAddress
		torrentPorts TorrentPorts
	)

	if err := pid.GetFrom(req); err != nil {
		return errors.Wrap(err, "Failed to read peer ID")
	}
	if err := xorAddr.GetFrom(req); err != nil {
		return errors.Wrap(err, "failed getting peer internal address")
	}
	if err := torrentPorts.GetFrom(req); err != nil {
		return errors.Wrap(err, "failed getting torrent-ports")
	}

	updated, err := s.updateSessionTable(addr, *pid, &xorAddr, torrentPorts)
	if err != nil {
		return errors.Wrap(err, "failed evaluating peer session")
	}
	if err := s.sendBindingSuccess(conn, *pid, req, res); err != nil {
		return errors.Wrap(err, "failed sending binding success response")
	}

	if updated {
		s.advertiseNewPeer(*pid, conn, res)
		s.advertiseSessionTableToPeer(*pid, res)
	}

	return nil
}

func (s *Server) sendBindingSuccess(conn net.PacketConn, pid PeerID, req, res *stun.Message) error {
	s.RLock()
	session, ok := s.peers[pid]
	if !ok {
		return fmt.Errorf("failed sendBindingSuccess: session of peer ID:%s does not exist", pid)
	}
	s.RUnlock()

	res.Reset()
	err := res.Build(
		stun.NewTransactionIDSetter(req.TransactionID),
		stun.BindingSuccess,
		&stun.XORMappedAddress{
			IP:   session[0].IP,
			Port: session[0].Port,
		},
		&s.ID,
		&SessionTable{},
		stun.NewShortTermIntegrity(s.cfg.StunPassword),
		stun.Fingerprint,
	)
	if err != nil {
		return errors.Wrapf(err, "failed building reply message for %s", pid)
	}
	if _, err = conn.WriteTo(res.Raw, session[0]); err != nil {
		return errors.Wrapf(err, "ERROR: WriteTo %s", session[0])
	}
	return nil
}

func (s *Server) updateSessionTable(
	addr net.Addr,
	pid PeerID,
	xorAddr *stun.XORMappedAddress,
	torrentPorts TorrentPorts,
) (bool, error) {
	s.Lock()
	defer s.Unlock()

	switch peer := addr.(type) {
	case *net.UDPAddr:
		session := Session{
			peer, // external IP/port
			&net.UDPAddr{ // internal IP/port
				IP:   xorAddr.IP,
				Port: xorAddr.Port,
			},
			&net.UDPAddr{ // torrent external IP/port
				IP:   peer.IP,
				Port: torrentPorts[0],
			},
			&net.UDPAddr{ // torrent internal IP/port
				IP:   xorAddr.IP,
				Port: torrentPorts[1],
			},
		}
		if old, ok := s.peers[pid]; ok && old.Equal(session) {
			return false, nil
		}
		s.peers[pid] = session
		log.Printf("Registered peer %s[%s,%s,%s,%s]", pid.String(), session[0].String(),
			session[1].String(), session[2].String(), session[3].String())
		return true, nil
	}
	return false, fmt.Errorf("unknown addr: %v", addr)
}

func (s *Server) advertiseNewPeer(pid PeerID, c net.PacketConn, msg *stun.Message) {
	s.RLock()
	defer s.RUnlock()

	session, ok := s.peers[pid]
	if !ok {
		return
	}

	msg.Reset()
	err := msg.Build(
		stun.TransactionID,
		stunBindingIndication,
		&s.ID,
		&SessionTable{pid: session},
		stun.NewShortTermIntegrity(s.cfg.StunPassword),
		stun.Fingerprint,
	)
	if err != nil {
		log.Printf("cannot build message to advertise new peer %s[%s][%s]: %v",
			pid.String(), session[0].String(), session[1].String(), err)
	}
	for ppid, paddrs := range s.peers {
		if ppid == pid {
			continue
		}
		if _, err = c.WriteTo(msg.Raw, paddrs[0]); err != nil {
			log.Printf("ERROR: WriteTo - %v", err)
		} else {
			log.Printf("advertise %s[%s][%s] to %s[%s][%s]",
				pid.String(), session[0].String(), session[1].String(),
				ppid.String(), paddrs[0].String(), paddrs[1].String())
		}
	}
}

func (s *Server) advertiseSessionTable() {
	s.RLock()
	defer s.RUnlock()

	msg := stunMessagePool.Get().(*stun.Message)
	for pid := range s.peers {
		s.advertiseSessionTableToPeer(pid, msg)
	}
	stunMessagePool.Put(msg)
}

func (s *Server) advertiseSessionTableToPeer(dest PeerID, msg *stun.Message) {
	msg.Reset()
	destAddrs, ok := s.peers[dest]
	if !ok {
		return
	}
	destAddr := destAddrs[0]

	nerr := 0
	for pid, sess := range s.peers {
		if pid == dest {
			continue
		}
		msg.Reset()
		err := msg.Build(
			stun.TransactionID,
			stunBindingIndication,
			&s.ID,
			&SessionTable{pid: sess},
			stun.NewShortTermIntegrity(s.cfg.StunPassword),
			stun.Fingerprint)
		if err != nil {
			nerr++
			continue
		}
		if _, err := s.udpConn.WriteToUDP(msg.Raw, destAddr); err != nil {
			nerr++
		}
	}
	log.Printf("sent session table to %s with %d failures", dest, nerr)
}

func (s *Server) saveUpdates() {
	s.Lock()
	defer s.Unlock()
	if !s.lastModified.After(s.lastSaved) {
		return
	}
	f, err := os.OpenFile(s.cfg.Database, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0640)
	if err == nil {
		err = json.NewEncoder(f).Encode(s.updates)
	}
	if err != nil {
		log.Printf("failed saving update database: %v", err)
	} else {
		s.lastSaved = s.lastModified
	}
}

func (s *Server) loadUpdates() error {
	s.Lock()
	defer s.Unlock()
	s.updates = make(map[string]*Notification)
	if _, err := os.Stat(s.cfg.Database); err != nil {
		// database file does not exist
		return nil
	}
	f, err := os.OpenFile(s.cfg.Database, os.O_RDONLY, 0640)
	if err == nil {
		err = json.NewDecoder(f).Decode(&s.updates)
	}
	return err
}
