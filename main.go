package main

import (
  "flag"
  "log"
  "sync"

  "github.com/gortc/stun"
  "github.com/pkg/errors"
)

var (
  address = flag.String("address", "0.0.0.0", "address to listen")
  port = flag.Int("port", 3478, "port to listen")
  stunServer = flag.Bool("stunServer", false, "enable STUN server")

  software = stun.NewSoftware("fruit/p2psecureupdate")
  errNonSTUNMessage = errors.New("Not STUN Message")
)

func main() {
  var wg sync.WaitGroup

  flag.Parse()

  if *stunServer {
    server := NewStunServer(*address, *port)
    wg.Add(1)
    go server.run(&wg)
  }

  wg.Wait()
  log.Println("The program is exiting.")
}
