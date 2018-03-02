package main

import (
	"fmt"
	"log"
	"net"
	"sync"

	"github.com/gortc/stun"
	"github.com/pkg/errors"
)

type StunServer struct {
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
	if err = s.processMessage(addr, buf[:n], req, res); err != nil {
		log.Printf("ERROR: processMessage - %v", err)
		return err
	}

	// Send a reply
	if _, err = c.WriteTo(res.Raw, addr); err != nil {
		log.Printf("ERROR: WriteTo - %v", err)
	}
	log.Printf("Reply sent: %v", res)

	return err
}

func (s *StunServer) processMessage(addr net.Addr, msg []byte, req, res *stun.Message) error {
	if !stun.IsMessage(msg) {
		return errNonSTUNMessage
	}
	// Convert the packet message to STUN message format
	if _, err := req.Write(msg); err != nil {
		return errors.Wrap(err, "Failed to read message")
	}

	if err := validateMessage(req, nil); err != nil {
		return errors.Wrap(err, "Invalid message")
	}

	if req.Type == stun.BindingRequest {
		return s.registerPeer(addr, req, res)
	}

	return nil
}

func (s *StunServer) registerPeer(addr net.Addr, req, res *stun.Message) error {
	// Extract Peer's ID, IP, and port from the message, then register it
	var username stun.Username
	if err := username.GetFrom(req); err != nil {
		return errors.Wrap(err, "Failed to read peer ID")
	}
	id := username.String()
	switch peer := addr.(type) {
	case *net.UDPAddr:
		s.peers[id] = Peer{
			ID:   id,
			IP:   peer.IP,
			Port: peer.Port,
		}
		log.Printf("Registered peer %s", s.peers[id].String())
	default:
		return fmt.Errorf("unknown addr: %v", addr)
	}
	return res.Build(
		stun.NewTransactionIDSetter(req.TransactionID),
		stun.NewType(stun.MethodBinding, stun.ClassSuccessResponse),
		stunSoftware,
		&stun.XORMappedAddress{
			IP:   s.peers[id].IP,
			Port: s.peers[id].Port,
		},
		stun.NewUsername(s.ID),
		s.peers,
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
}
