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
		var (
			id      string
			overlay *Overlay
			err     error
		)
		if id, err = localID(); err != nil {
			log.Println("Cannot get local id:", err)
		}
		if overlay, err = NewOverlay(id, *stunServerAddrConnect, nil); err != nil {
			log.Println("Cannot crete overlay:", err)
		} else if err = overlay.Open(); err != nil {
			log.Println("Cannot open overlay:", err)
		}
		time.Sleep(10 * time.Minute)
		log.Println("overlay's state:", overlay.automata.current)
		time.Sleep(time.Second)
	}

	wg.Wait()
	log.Println("The program is exiting.")
}
