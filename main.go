package main

import (
  "flag"
  "log"
  "fmt"
  "net"

  "github.com/gortc/stun"
  "github.com/pkg/errors"
)

var (
  address = flag.String("address", "0.0.0.0", "address to listen")
  port = flag.Int("port", 3478, "port to listen")

  software = stun.NewSoftware("fruit/p2psecureupdate")
  errNonSTUNMessage = errors.New("Not STUN Message")
)

type Server struct {
  Address string
  Port    int
}

func (s *Server) run() error {
  addrPort := fmt.Sprintf("%s:%d", s.Address, s.Port)
  conn, err := net.ListenPacket("udp", addrPort)
  if err != nil {
    return err
  }
  log.Printf("Serving at %s:%d", s.Address, s.Port)
  s.serve(conn)
  return nil
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
  return nil
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
  // Process the STUN message, then create a response
  if err = s.processMessage(addr, buf[:n], req, res); err != nil {
    if err == errNonSTUNMessage {
      return nil
    }
    log.Printf("ERROR: processMessage - %v", err)
    return err
  }
  _, err = c.WriteTo(res.Raw, addr)
  if err != nil {
    log.Printf("ERROR: WriteTo - %v", err)
  }
  return err
}

func (s *Server) processMessage(addr net.Addr, msg []byte, req, res *stun.Message) error {
  if !stun.IsMessage(msg) {
    return errNonSTUNMessage
  }
  // Convert the packet message to STUN message format
  if _, err := req.Write(msg); err != nil {
    return errors.Wrap(err, "Failed to read message")
  }

  var (
    ip net.IP
    port int
  )
  switch peer := addr.(type) {
  case *net.UDPAddr:
    ip = peer.IP
    port = peer.Port
  default:
    panic(fmt.Sprintf("unknown addr: %v", addr))
  }
  log.Printf("Receive packet from %v:%d", ip, port)
  return res.Build(
    stun.NewTransactionIDSetter(req.TransactionID),
    stun.NewType(stun.MethodBinding, stun.ClassSuccessResponse),
    software,
    &stun.XORMappedAddress {
      IP: ip,
      Port: port,
    },
    stun.Fingerprint,
  )
}

func main() {
  flag.Parse()
  server := Server {
    Address: *address,
    Port: *port,
  }
  server.run()
}
