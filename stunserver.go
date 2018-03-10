// Copyright 2018 University of Glasgow.
// Use of this source code is governed by an Apache
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"sync"
	"time"

	"github.com/gortc/stun"
	"github.com/pkg/errors"
)

const (
	defaultSessionTableAdvertiseDuration = 60 * time.Second
)

// ServerConfig contains the server configuration parameters.
type ServerConfig struct {
	Address      string `json:"address"`
	SessionTable struct {
		AdvertiseDuration time.Duration `json:"advertise-duration"`
	} `json:"session-table"`
	Redis struct {
		Address string `json:"address"`
	} `json:"redis"`
}

// NewServerConfigFromFile loads and returns configurations from given JSON
// file.
func NewServerConfigFromFile(f string) (*ServerConfig, error) {
	var cfg ServerConfig

	if f != "" {
		raw, err := ioutil.ReadFile(f)
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &cfg); err != nil {
			return nil, err
		}
	}
	return &cfg, nil
}

func (cfg *ServerConfig) check() {
	if cfg.SessionTable.AdvertiseDuration == 0 {
		cfg.SessionTable.AdvertiseDuration = defaultSessionTableAdvertiseDuration
	}
	if cfg.Redis.Address == "" {
		cfg.Redis.Address = "127.0.0.1:6379"
	}
}

// StunServer is a STUN server implementation for multicast messaging system
type StunServer struct {
	sync.RWMutex
	Addr  *net.UDPAddr
	ID    PeerID
	peers SessionTable
	cfg   *ServerConfig
}

// NewStunServer returns an instance of StunServer
func NewStunServer(address string, cfg *ServerConfig) (*StunServer, error) {
	var (
		id   *PeerID
		addr *net.UDPAddr
		err  error
	)

	if cfg == nil {
		cfg = &ServerConfig{}
	}
	cfg.check()
	if cfg.Address == "" {
		cfg.Address = address
	}
	if addr, err = net.ResolveUDPAddr("udp", cfg.Address); err != nil {
		return nil, errors.Wrapf(err, "failed resolving address %s", address)
	}
	if id, err = LocalPeerID(); err != nil {
		return nil, errors.Wrap(err, "Cannot get local ID")
	}
	return &StunServer{
		Addr:  addr,
		ID:    *id,
		peers: make(SessionTable),
		cfg:   cfg,
	}, nil
}

func (s *StunServer) run(wg *sync.WaitGroup) {
	defer wg.Done()

	conn, err := net.ListenUDP("udp", s.Addr)
	if err != nil {
		log.Println(err)
		return
	}

	go func() {
		log.Printf("start a thread that advertises session table every %ss",
			s.cfg.SessionTable.AdvertiseDuration)
		for {
			time.Sleep(s.cfg.SessionTable.AdvertiseDuration)
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

func (s *StunServer) serve(c net.PacketConn) error {
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

func (s *StunServer) serveConn(c net.PacketConn, res, req *stun.Message) error {
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

func (s *StunServer) processMessage(addr net.Addr, msg []byte, req, res *stun.Message) (*PeerID, error) {
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

func (s *StunServer) registerPeer(addr net.Addr, req, res *stun.Message) (*PeerID, error) {
	// Extract Peer's ID, IP, and port from the message, then register it
	var (
		pid     = new(PeerID)
		xorAddr stun.XORMappedAddress
	)

	if err := pid.GetFrom(req); err != nil {
		return nil, errors.Wrap(err, "Failed to read peer ID")
	}
	if err := xorAddr.GetFrom(req); err != nil {
		return nil, errors.Wrap(err, "failed getting peer internal address")
	}

	s.Lock()
	defer s.Unlock()
	switch peer := addr.(type) {
	case *net.UDPAddr:
		s.peers[*pid] = []*net.UDPAddr{
			peer,
			&net.UDPAddr{
				IP:   xorAddr.IP,
				Port: xorAddr.Port,
			},
		}
		log.Printf("Registered peer %s[%s][%s]", pid.String(), s.peers[*pid][0].String(),
			s.peers[*pid][1].String())
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

func (s *StunServer) advertiseNewPeer(pid *PeerID, addrs []*net.UDPAddr, c net.PacketConn) {
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

func (s *StunServer) advertiseSessionTable(c net.PacketConn) error {
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
