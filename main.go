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
    server := NewStunServer(*stunServerAddrListen)
    wg.Add(1)
    go server.run(&wg)
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
      if ok, err := ValidMessage(res.Message); err != nil {
        log.Println("failed to validate message", err)
      } else if !ok {
        log.Println("invalid message")
      } else if res.Message.Type.Method == stun.MethodRefresh &&
          res.Message.Type.Class == stun.ClassSuccessResponse {
        log.Println("got reply from server")
      } else {
        log.Println("invalid message method/class")
      }
    }
    if err = client.Ping(*stunServerAddrConnect, callback); err != nil {
      log.Fatal(err)
    }
  }

  wg.Wait()
  log.Println("The program is exiting.")
}
