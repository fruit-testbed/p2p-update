package main

import (
  "flag"
  "log"
  "sync"
  "time"
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
    if server, err := NewStunServer(*stunServerAddrListen); err == nil {
      wg.Add(1)
      go server.run(&wg)
    } else {
      log.Fatalln("Failed starting the STUN server: %v", err)
    }
  }

  if !*disabledClient {
    c, err := NewStunClient()
    if err == nil {
      if err = c.Start(*stunServerAddrConnect); err != nil {
        log.Fatal(err)
      }
      time.Sleep(10000 * time.Millisecond)
      c.Stop()
      time.Sleep(1000 * time.Millisecond)
      log.Println(c.State)
    } else {
      log.Fatal(err)
    }
  }

  wg.Wait()
  log.Println("The program is exiting.")
}
