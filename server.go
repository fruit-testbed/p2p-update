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

	conn, err := net.ListenUDP("udp", s.Addr)
	if err != nil {
		log.Printf("failed listening UDP at %s - %v", s.Addr.String(), err)
		return
	}
	s.udpConn = conn

	ExecEvery(time.Duration(s.cfg.SessionAdvertiseTime)*time.Second, s.advertiseSessionTable)
	ExecEvery(time.Duration(s.cfg.SnapshotTime)*time.Second, s.saveUpdates)

	go s.serveTCP()

	if err = s.serve(conn); err != nil {
		log.Println(err)
	}
}

func (s *Server) serveTCP() {
	if err := fasthttp.ListenAndServe(s.Addr.String(), s.serveHTTPRequest); err != nil {
		log.Fatalf("failed serving TCP at %s - %v", s.Addr.String(), err)
	}
	log.Printf("Serving HTTP at %s", s.Addr.String())
}

func (s *Server) serveHTTPRequest(ctx *fasthttp.RequestCtx) {
	switch {
	case bytes.Compare(ctx.Method(), strGET) == 0:
		s.RLock()
		doJSONWrite(ctx, 200, s.updates)
		s.RUnlock()
		return
	case bytes.Compare(ctx.Method(), strPOST) == 0:
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
		return
	}
	ctx.SetStatusCode(400)
}

func (s *Server) serve(c net.PacketConn) error {
	var (
		res = new(stun.Message)
		req = new(stun.Message)
	)

	log.Printf("Serving at %s with id:%s", s.Addr.String(), s.ID.String())
	for {
		if err := s.serveConn(c, res, req); err != nil {
			log.Printf("WARNING: Served connection with error - %v", err)
		}
		res.Reset()
		req.Reset()
	}
}

func (s *Server) serveConn(c net.PacketConn, res, req *stun.Message) error {
	if c == nil {
		return nil
	}

	// Read the packet message
	buf := make([]byte, 1024)
	n, addr, err := c.ReadFrom(buf)
	if err != nil {
		log.Printf("ERROR: ReadFrom %v - %v", addr, err)
		return err
	}

	// Process the STUN message
	if err = s.processMessage(c, addr, buf[:n], req, res); err != nil {
		return errors.Wrapf(err, "ERROR: processMessage from %s", addr.String())
	}
	return err
}

func (s *Server) processMessage(c net.PacketConn, addr net.Addr, msg []byte, req, res *stun.Message) error {
	if !stun.IsMessage(msg) {
		return errNonSTUNMessage
	}
	// Convert the packet message to STUN message format
	if _, err := req.Write(msg); err != nil {
		return errors.Wrap(err, "Failed to read message")
	}
	if err := validateMessage(req, nil, s.cfg.StunPassword); err != nil {
		return errors.Wrap(err, "Invalid message")
	}
	if req.Type == stun.BindingRequest {
		return s.registerPeer(c, addr, req, res)
	}
	return fmt.Errorf("message type is not STUN binding")
}

func (s *Server) registerPeer(c net.PacketConn, addr net.Addr, req, res *stun.Message) error {
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
		if old, ok := s.peers[*pid]; ok && old.Equal(session) {
			// nothing to update
			return nil
		}
		s.peers[*pid] = session
		log.Printf("Registered peer %s[%s,%s,%s,%s]", pid.String(), s.peers[*pid][0].String(),
			s.peers[*pid][1].String(), s.peers[*pid][2].String(), s.peers[*pid][3].String())
	default:
		return fmt.Errorf("unknown addr: %v", addr)
	}

	err := res.Build(
		stun.NewTransactionIDSetter(req.TransactionID),
		stun.BindingSuccess,
		&stun.XORMappedAddress{
			IP:   s.peers[*pid][0].IP,
			Port: s.peers[*pid][0].Port,
		},
		&s.ID,
		&s.peers,
		stun.NewShortTermIntegrity(s.cfg.StunPassword),
		stun.Fingerprint,
	)
	if err != nil {
		return errors.Wrapf(err, "failed building reply message for %s", *pid)
	}

	if _, err = c.WriteTo(res.Raw, addr); err != nil {
		return errors.Wrapf(err, "ERROR: WriteTo %s", addr)
	}

	go s.advertiseNewPeer(pid, s.peers[*pid], c)

	return nil
}

func (s *Server) advertiseNewPeer(pid *PeerID, sess Session, c net.PacketConn) {
	msg, err := stun.Build(
		stun.TransactionID,
		stunBindingIndication,
		&s.ID,
		&SessionTable{*pid: sess},
		stun.NewShortTermIntegrity(s.cfg.StunPassword),
		stun.Fingerprint,
	)
	if err != nil {
		log.Printf("cannot build message to advertise new peer %s[%s][%s]: %v",
			pid.String(), sess[0].String(), sess[1].String(), err)
		return
	}
	for ppid, paddrs := range s.peers {
		if ppid == *pid {
			continue
		}
		if _, err = c.WriteTo(msg.Raw, paddrs[0]); err != nil {
			log.Printf("ERROR: WriteTo - %v", err)
		} else {
			log.Printf("advertise %s[%s][%s] to %s[%s][%s]",
				pid.String(), sess[0].String(), sess[1].String(),
				ppid.String(), paddrs[0].String(), paddrs[1].String())
		}
	}
}

func (s *Server) advertiseSessionTable() {
	s.RLock()
	defer s.RUnlock()
	msg, err := stun.Build(
		stun.TransactionID,
		stunBindingIndication,
		&s.ID,
		&s.peers,
		stun.NewShortTermIntegrity(s.cfg.StunPassword),
		stun.Fingerprint,
	)
	if err != nil {
		log.Printf("cannot build message to advertise session table: %v", err)
	} else {
		nerr := 0
		for id, addrs := range s.peers {
			if _, err = s.udpConn.WriteTo(msg.Raw, addrs[0]); err != nil {
				log.Printf("WARNING: failed sent session table message to %s[%s][%s]: %v",
					id, addrs[0].String(), addrs[1].String(), err)
				nerr++
			}
		}
		log.Printf("sent session table to %d peers with %d failures", len(s.peers), nerr)
	}
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
