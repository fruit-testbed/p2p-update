package main

import (
  "flag"
  "log"
  "sync"
)

var (
  enabledStunServer = flag.Bool("enabledStunServer", false,
      "enabled STUN server")
  stunServerAddrListen = flag.String("stunServerListen", "0.0.0.0:3478",
      "[address]:[port] of STUN server to listen")
  stunServerAddrConnect = flag.String("stunServerConnect",
      "fruit-testbed.org:3478", "[address]:[port] of STUN server to connect")
  disabledClient = flag.Bool("disabledClient", false, "disabled client")
)

func main() {
  var wg sync.WaitGroup

  flag.Parse()

  if *enabledStunServer {
    server := NewStunServer(*stunServerAddrListen)
    wg.Add(1)
    go server.run(&wg)
  }

  if !*disabledClient {
    client := NewStunClient()
    if err := client.Dial(*stunServerAddrConnect); err != nil {
      log.Fatal(err)
    }
  }

  wg.Wait()
  log.Println("The program is exiting.")
}
