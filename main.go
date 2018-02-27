package main

import (
  "flag"
  "log"
  "sync"

  "github.com/gortc/stun"
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
    client, err := NewStunClient()
    if err != nil {
      log.Fatal(err)
    }
    callback := func(res stun.Event) {
      if res.Error != nil {
        log.Fatal(res.Error)
      }
      if err := ValidateMessage(res.Message, &stunTypeRefreshSuccess); err != nil {
        log.Println("invalid message", err)
      } else {
        log.Println("got a reply from server")
      }
    }
    if err = client.Ping(*stunServerAddrConnect, callback); err != nil {
      log.Fatal(err)
    }
  }

  wg.Wait()
  log.Println("The program is exiting.")
}
