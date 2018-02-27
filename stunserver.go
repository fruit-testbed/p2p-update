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
  return fmt.Sprintf("%s[%v:%d]", p.Id, p.IP, p.Port)
}

type StunServer struct {
  Address string
  Username string
  peers   map[string]*Peer
}

func NewStunServer(address string) StunServer {
  return StunServer {
    Address: address,
    Username: "fruit",
    peers: make(map[string]*Peer),
  }
}

func (s *StunServer) run(wg *sync.WaitGroup) error {
  defer wg.Done()

  conn, err := net.ListenPacket("udp", s.Address)
  if err != nil {
    return err
  }
  log.Printf("Serving at %s", s.Address)
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

  if ok, err := ValidMessage(req); err != nil {
    return false, errors.Wrap(err, "Invalid message")
  } else if !ok {
    return false, errors.New("Invalid message")
  }

  if req.Type.Method == stun.MethodRefresh &&
      req.Type.Class == stun.ClassRequest {
    return true, s.replyPing(addr, req, res)
  } else if req.Type.Method == stun.MethodBinding &&
      req.Type.Class == stun.ClassRequest {
    return false, s.registerPeer(addr, req)
  }

  return false, nil
}

func (s *StunServer) replyPing(addr net.Addr, req, res *stun.Message) error {
  return res.Build(
    stun.NewTransactionIDSetter(req.TransactionID),
		stunSoftware,
		stun.NewUsername(s.Username),
		stun.NewLongTermIntegrity(s.Username, stunRealm, stunPassword),
		stun.NewType(stun.MethodBinding, stun.ClassSuccessResponse),
		stun.Fingerprint,
  )
}

func (s *StunServer) registerPeer(addr net.Addr, req *stun.Message) error {
  // Extract Peer's ID, IP, and port from the message, then register it
  var username stun.Username
  if err := username.GetFrom(req); err != nil {
    return errors.Wrap(err, "Failed to read peer ID")
  }
  switch peer := addr.(type) {
  case *net.UDPAddr:
    id := username.String()
    p := &Peer {
      Id: id,
      IP: peer.IP,
      Port: peer.Port,
    }
    s.peers[id] = p
    log.Printf("Registered peer %s", p.String())
  default:
    return errors.New(fmt.Sprintf("unknown addr: %v", addr))
  }
  return nil
}
