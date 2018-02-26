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
  Port    int
}

func (s *StunServer) run(wg *sync.WaitGroup) error {
  defer wg.Done()

  addrPort := fmt.Sprintf("%s:%d", s.Address, s.Port)
  conn, err := net.ListenPacket("udp", addrPort)
  if err != nil {
    return err
  }
  log.Printf("Serving at %s:%d", s.Address, s.Port)
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

func (s *StunServer) processMessage(addr net.Addr, msg []byte, req, res *stun.Message) error {
  if !stun.IsMessage(msg) {
    return errNonSTUNMessage
  }
  // Convert the packet message to STUN message format
  if _, err := req.Write(msg); err != nil {
    return errors.Wrap(err, "Failed to read message")
  }

  // external IP and port
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

  var xorAddr stun.XORMappedAddress
  if err := xorAddr.GetFrom(req); err != nil {
    log.Println(err)
  }
  var soft stun.Software
  if err := soft.GetFrom(req); err != nil {
    log.Println("Software.GetFrom", err)
  }
  log.Println(xorAddr, soft)
  log.Println(req.Raw)

  // Build and return response message
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
