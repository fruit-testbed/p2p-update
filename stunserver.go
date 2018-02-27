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
  peers   map[string]*Peer
}

func NewStunServer(address string) StunServer {
  return StunServer {
    Address: address,
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

  // Extract Peer's ID, IP, and port from the message
  var username stun.Username
  if err := username.GetFrom(req); err != nil {
    return false, errors.Wrap(err, "Failed to read peer ID")
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
    return false, errors.New(fmt.Sprintf("unknown addr: %v", addr))
  }

  return false, nil
}
