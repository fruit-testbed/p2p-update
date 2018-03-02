package main

import (
	"flag"
	"log"
	"sync"
	"time"

	"github.com/gortc/stun"
)

var (
	stunServerMode = flag.Bool("stun", false, "server mode")
	address        = flag.String("addr", "", "STUN server address e.g. fruit-testbed.org:3478 (client mode), listen address (server mode), or data destination address (send mode)")
	sendData       = flag.String("send", "", "data to be sent")
)

func main() {
	var wg sync.WaitGroup

	flag.Parse()

	if *stunServerMode {
		if server, err := NewStunServer(*address); err == nil {
			wg.Add(1)
			go server.run(&wg)
		} else {
			log.Fatalln("Failed starting the STUN server: %v", err)
		}
		wg.Wait()
		log.Println("Server is exiting.")
	} else if *sendData != "" {
		c, err := stun.Dial("udp", *address)
		if err != nil {
			log.Fatalln("Failed dialing to destination", *address)
		}
		msg := stun.MustBuild(
			stun.TransactionID,
			stunDataRequest,
			stunSoftware,
			stun.NewUsername("sender"),
		)
		msg.Add(stun.AttrData, []byte(*sendData))
		stun.NewShortTermIntegrity(stunPassword).AddTo(msg)
		stun.Fingerprint.AddTo(msg)
		c.Do(msg, time.Now().Add(5*time.Second), func(e stun.Event) {
			log.Println(e.Error)
			log.Println(e.Message)
		})
	} else {
		var (
			id      string
			overlay *Overlay
			err     error
		)
		if id, err = localID(); err != nil {
			log.Fatalln("Cannot get local id:", err)
		}
		if overlay, err = NewOverlay(id, *address, nil); err != nil {
			log.Fatalln("Cannot crete overlay:", err)
		}
		overlay.DataHandler = overlay
		if err = overlay.Open(); err != nil {
			log.Fatalln("Cannot open overlay:", err)
		}
		time.Sleep(10 * time.Minute)
		log.Println("overlay's state:", overlay.automata.current)
		time.Sleep(time.Second)
	}
}
