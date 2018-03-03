package main

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/gortc/stun"
	"github.com/pkg/errors"
)

const (
	advertiseSessionTableSleepTime = 60 * time.Second
)

type StunServer struct {
	sync.RWMutex
	Address string
	ID      string
	peers   SessionTable
}

func NewStunServer(address string) (*StunServer, error) {
	var id string
	var err error
	if id, err = localID(); err != nil {
		return nil, errors.Wrap(err, "Cannot get local ID")
	}
	return &StunServer{
		Address: address,
		ID:      id,
		peers:   make(SessionTable),
	}, nil
}

func (s *StunServer) run(wg *sync.WaitGroup) {
	defer wg.Done()

	conn, err := net.ListenPacket("udp", s.Address)
	if err != nil {
		log.Println(err)
		return
	}

	go func() {
		log.Printf("start a thread that advertises session table every %ss",
			advertiseSessionTableSleepTime)
		for {
			time.Sleep(advertiseSessionTableSleepTime)
			if err := s.advertiseSessionTable(conn); err != nil {
				log.Println(err)
			}
		}
	}()

	log.Printf("Serving at %s with id:%s", s.Address, s.ID)
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
	var peerID string
	if peerID, err = s.processMessage(addr, buf[:n], req, res); err != nil {
		log.Printf("ERROR: processMessage - %v", err)
		return err
	}

	// Send a reply
	if _, err = c.WriteTo(res.Raw, addr); err != nil {
		log.Printf("ERROR: WriteTo - %v", err)
	}
	log.Printf("Reply sent: %v", res)

	go s.advertiseNewPeer(s.peers[peerID], c)

	return err
}

func (s *StunServer) processMessage(addr net.Addr, msg []byte, req, res *stun.Message) (string, error) {
	if !stun.IsMessage(msg) {
		return "", errNonSTUNMessage
	}
	// Convert the packet message to STUN message format
	if _, err := req.Write(msg); err != nil {
		return "", errors.Wrap(err, "Failed to read message")
	}

	if err := validateMessage(req, nil); err != nil {
		return "", errors.Wrap(err, "Invalid message")
	}

	if req.Type == stun.BindingRequest {
		return s.registerPeer(addr, req, res)
	}

	return "", nil
}

func (s *StunServer) registerPeer(addr net.Addr, req, res *stun.Message) (string, error) {
	// Extract Peer's ID, IP, and port from the message, then register it
	var (
		username stun.Username
		xorAddr  stun.XORMappedAddress
	)

	if err := username.GetFrom(req); err != nil {
		return "", errors.Wrap(err, "Failed to read peer ID")
	}
	if err := xorAddr.GetFrom(req); err != nil {
		return "", errors.Wrap(err, "failed getting peer internal address")
	}

	id := username.String()
	switch peer := addr.(type) {
	case *net.UDPAddr:
		s.peers[id] = Peer{
			ID:           id,
			ExternalAddr: *peer,
			InternalAddr: net.UDPAddr{
				IP:   xorAddr.IP,
				Port: xorAddr.Port,
			},
		}
		log.Printf("Registered peer %s", s.peers[id].String())
	default:
		return "", fmt.Errorf("unknown addr: %v", addr)
	}
	return id, res.Build(
		stun.NewTransactionIDSetter(req.TransactionID),
		stun.NewType(stun.MethodBinding, stun.ClassSuccessResponse),
		stunSoftware,
		&stun.XORMappedAddress{
			IP:   s.peers[id].ExternalAddr.IP,
			Port: s.peers[id].ExternalAddr.Port,
		},
		stun.NewUsername(s.ID),
		s.peers,
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
}

func (s *StunServer) advertiseNewPeer(newPeer Peer, c net.PacketConn) {
	msg, err := stun.Build(
		stun.TransactionID,
		stunBindingIndication,
		stunSoftware,
		stun.NewUsername(s.ID),
		SessionTable{newPeer.ID: newPeer},
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
	if err != nil {
		log.Printf("cannot build message to advertise new peer %s: %v", newPeer, err)
		return
	}
	for _, peer := range s.peers {
		if peer.ID == newPeer.ID {
			continue
		}
		if _, err = c.WriteTo(msg.Raw, &peer.ExternalAddr); err != nil {
			log.Printf("ERROR: WriteTo - %v", err)
		} else {
			log.Printf("advertise %s to %s", newPeer.String(), peer.String())
		}
	}
}

func (s *StunServer) advertiseSessionTable(c net.PacketConn) error {
	s.RLock()
	peers := s.peers
	s.RUnlock()
	msg, err := stun.Build(
		stun.TransactionID,
		stunBindingIndication,
		stunSoftware,
		stun.NewUsername(s.ID),
		peers,
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
	if err != nil {
		errors.Wrap(err, "cannot build message to advertise session table: %v")
	}
	nerr := 0
	for _, peer := range peers {
		if _, err = c.WriteTo(msg.Raw, &peer.ExternalAddr); err != nil {
			log.Printf("WARNING: failed sent session table message to %s: %v", peer.String(), err)
			nerr++
		}
	}
	log.Printf("sent session table to %d peers with %d failures", len(peers), nerr)
	return nil
}
