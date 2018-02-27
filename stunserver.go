package main

import (
  "fmt"
  "log"
  "net"
  "sync"

  "github.com/gortc/stun"
  "github.com/pkg/errors"
)

type Peer struct {
  Id string
  IP net.IP
  Port int
}

func (p *Peer) String() string {
  return fmt.Sprintf("%s[%v[%d]]", p.Id, p.IP, p.Port)
}

type StunServer struct {
  Address string
  Id string
  peers   map[string]*Peer
}

func NewStunServer(address string) (*StunServer, error) {
  var id string
  var err error
  if id, err = localId(); err != nil {
    return nil, errors.Wrap(err, "Cannot get local id")
  }
  return &StunServer {
    Address: address,
    Id: id,
    peers: make(map[string]*Peer),
  }, nil
}

func (s *StunServer) run(wg *sync.WaitGroup) error {
  defer wg.Done()

  conn, err := net.ListenPacket("udp", s.Address)
  if err != nil {
    return err
  }
  log.Printf("Serving at %s with id:%s", s.Address, s.Id)
  s.serve(conn)
  return nil
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
  return nil
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
  var reply bool
  if reply, err = s.processMessage(addr, buf[:n], req, res); err != nil {
    if err == errNonSTUNMessage {
      return nil
    }
    log.Printf("ERROR: processMessage - %v", err)
    return err
  } else if reply {
    _, err = c.WriteTo(res.Raw, addr)
    if err != nil {
      log.Printf("ERROR: WriteTo - %v", err)
    }
  }

  return err
}

func (s *StunServer) processMessage(addr net.Addr, msg []byte, req, res *stun.Message) (bool, error) {
  if !stun.IsMessage(msg) {
    return false, errNonSTUNMessage
  }
  // Convert the packet message to STUN message format
  if _, err := req.Write(msg); err != nil {
    return false, errors.Wrap(err, "Failed to read message")
  }

  if err := ValidateMessage(req, nil); err != nil {
    return false, errors.Wrap(err, "Invalid message")
  }

  if req.Type.Method == stun.MethodRefresh &&
      req.Type.Class == stun.ClassRequest {
    return true, s.replyPing(addr, req, res)
  } else if req.Type.Method == stun.MethodBinding &&
      req.Type.Class == stun.ClassRequest {
    return true, s.registerPeer(addr, req, res)
  }
  log.Printf("not replying: %v", *req)

  return false, nil
}

func (s *StunServer) replyPing(addr net.Addr, req, res *stun.Message) error {
  switch peer := addr.(type) {
  case *net.UDPAddr:
    log.Printf("Received ping from peer %v[%d]", peer.IP, peer.Port)
  default:
    return errors.New(fmt.Sprintf("unknown addr: %v", addr))
  }

  return res.Build(
    stun.NewTransactionIDSetter(req.TransactionID),
    stun.NewType(stun.MethodRefresh, stun.ClassSuccessResponse),
		stunSoftware,
		stun.NewUsername(s.Id),
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
  )
}

func (s *StunServer) registerPeer(addr net.Addr, req, res *stun.Message) error {
  // Extract Peer's ID, IP, and port from the message, then register it
  var username stun.Username
  if err := username.GetFrom(req); err != nil {
    return errors.Wrap(err, "Failed to read peer ID")
  }
  p := &Peer{}
  switch peer := addr.(type) {
  case *net.UDPAddr:
    p.Id = username.String()
    p.IP = peer.IP
    p.Port = peer.Port
    s.peers[p.Id] = p
    log.Printf("Registered peer %s", p.String())
  default:
    return errors.New(fmt.Sprintf("unknown addr: %v", addr))
  }
  return res.Build(
    stun.NewTransactionIDSetter(req.TransactionID),
    stun.NewType(stun.MethodBinding, stun.ClassSuccessResponse),
		stunSoftware,
    &stun.XORMappedAddress {
      IP: p.IP,
      Port: p.Port,
    },
		stun.NewUsername(s.Id),
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
  )
}
