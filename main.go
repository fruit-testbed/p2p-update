package main

import (
  "flag"

  "github.com/gortc/stun"
  "github.com/pkg/errors"
)

var (
  address = flag.String("address", "0.0.0.0", "address to listen")
  port = flag.Int("port", 3478, "port to listen")

  software = stun.NewSoftware("fruit/p2psecureupdate")
  errNonSTUNMessage = errors.New("Not STUN Message")
)

func main() {
  flag.Parse()
  server := StunServer {
    Address: *address,
    Port: *port,
  }
  server.run()
}
