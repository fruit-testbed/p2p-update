// Copyright 2018 University of Glasgow.
// Use of this source code is governed by an Apache
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/gortc/stun"
	"github.com/pkg/errors"
)

// ServerConfig contains the server configuration parameters.
type ServerConfig struct {
	Address              string `json:"address"`
	SessionAdvertiseTime int    `json:"session-advertise-time"` // in seconds
}

// DefaultServerConfig returns default server configurations.
func DefaultServerConfig() *ServerConfig {
	cfg := &ServerConfig{
		Address:              "",
		SessionAdvertiseTime: 60,
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
}

// NewServer returns an instance of Server
func NewServer(cfg ServerConfig) (*Server, error) {
	var (
		id   *PeerID
		addr *net.UDPAddr
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
	s := &Server{
		Addr:  addr,
		ID:    *id,
		peers: make(SessionTable),
		cfg:   &cfg,
	}

	j, _ = json.Marshal(s.cfg)
	log.Printf("created server with config: %s", string(j))

	return s, nil
}

func (s *Server) run(wg *sync.WaitGroup) {
	defer wg.Done()

	conn, err := net.ListenUDP("udp", s.Addr)
	if err != nil {
		log.Println(err)
		return
	}

	go func() {
		d, _ := time.ParseDuration(fmt.Sprintf("%ds", s.cfg.SessionAdvertiseTime))
		log.Printf("start a thread that advertises session table every %s", d)
		for {
			time.Sleep(d)
			if err := s.advertiseSessionTable(conn); err != nil {
				log.Println(err)
			}
		}
	}()

	log.Printf("Serving at %s with id:%s", s.Addr.String(), s.ID.String())
	if err = s.serve(conn); err != nil {
		log.Println(err)
	}
}

func (s *Server) serve(c net.PacketConn) error {
	var (
		res = new(stun.Message)
		req = new(stun.Message)
	)
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
	var pid *PeerID
	if pid, err = s.processMessage(addr, buf[:n], req, res); err != nil {
		log.Printf("ERROR: processMessage - %v", err)
		return err
	}

	// Send a reply
	if _, err = c.WriteTo(res.Raw, addr); err != nil {
		log.Printf("ERROR: WriteTo - %v", err)
	}
	log.Printf("Reply sent: %v", res)

	go s.advertiseNewPeer(pid, s.peers[*pid], c)

	return err
}

func (s *Server) processMessage(addr net.Addr, msg []byte, req, res *stun.Message) (*PeerID, error) {
	if !stun.IsMessage(msg) {
		return nil, errNonSTUNMessage
	}
	// Convert the packet message to STUN message format
	if _, err := req.Write(msg); err != nil {
		return nil, errors.Wrap(err, "Failed to read message")
	}
	if err := validateMessage(req, nil); err != nil {
		return nil, errors.Wrap(err, "Invalid message")
	}
	if req.Type == stun.BindingRequest {
		return s.registerPeer(addr, req, res)
	}
	return nil, fmt.Errorf("message type is not STUN binding")
}

func (s *Server) registerPeer(addr net.Addr, req, res *stun.Message) (*PeerID, error) {
	// Extract Peer's ID, IP, and port from the message, then register it
	var (
		pid          = new(PeerID)
		xorAddr      stun.XORMappedAddress
		torrentPorts TorrentPorts
	)

	if err := pid.GetFrom(req); err != nil {
		return nil, errors.Wrap(err, "Failed to read peer ID")
	}
	if err := xorAddr.GetFrom(req); err != nil {
		return nil, errors.Wrap(err, "failed getting peer internal address")
	}
	if err := torrentPorts.GetFrom(req); err != nil {
		return nil, errors.Wrap(err, "failed getting torrent-ports")
	}

	s.Lock()
	defer s.Unlock()
	switch peer := addr.(type) {
	case *net.UDPAddr:
		s.peers[*pid] = []*net.UDPAddr{
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
		log.Printf("Registered peer %s[%s,%s,%s,%s]", pid.String(), s.peers[*pid][0].String(),
			s.peers[*pid][1].String(), s.peers[*pid][2].String(), s.peers[*pid][3].String())
	default:
		return nil, fmt.Errorf("unknown addr: %v", addr)
	}
	return pid, res.Build(
		stun.NewTransactionIDSetter(req.TransactionID),
		stun.NewType(stun.MethodBinding, stun.ClassSuccessResponse),
		&stun.XORMappedAddress{
			IP:   s.peers[*pid][0].IP,
			Port: s.peers[*pid][0].Port,
		},
		&s.ID,
		&s.peers,
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
}

func (s *Server) advertiseNewPeer(pid *PeerID, addrs []*net.UDPAddr, c net.PacketConn) {
	msg, err := stun.Build(
		stun.TransactionID,
		stunBindingIndication,
		&s.ID,
		&SessionTable{*pid: addrs},
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
	if err != nil {
		log.Printf("cannot build message to advertise new peer %s[%s][%s]: %v",
			pid.String(), addrs[0].String(), addrs[1].String(), err)
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
				pid.String(), addrs[0].String(), addrs[1].String(),
				ppid.String(), paddrs[0].String(), paddrs[1].String())
		}
	}
}

func (s *Server) advertiseSessionTable(c net.PacketConn) error {
	s.RLock()
	defer s.RUnlock()
	msg, err := stun.Build(
		stun.TransactionID,
		stunBindingIndication,
		&s.ID,
		&s.peers,
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
	if err != nil {
		errors.Wrap(err, "cannot build message to advertise session table: %v")
	}
	nerr := 0
	for id, addrs := range s.peers {
		if _, err = c.WriteTo(msg.Raw, addrs[0]); err != nil {
			log.Printf("WARNING: failed sent session table message to %s[%s][%s]: %v",
				id, addrs[0].String(), addrs[1].String(), err)
			nerr++
		}
	}
	log.Printf("sent session table to %d peers with %d failures", len(s.peers), nerr)
	return nil
}
