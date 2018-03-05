package main

import (
	"flag"
	"log"
	"net"
	"sync"
	"time"

	"github.com/gortc/stun"
)

var (
	stunServerMode = flag.Bool("stun", false, "server mode")
	remoteAddr     = flag.String("raddr", "", "remote address: STUN server address (client mode), listen address (server mode), or destination address (send mode)")
	localAddr      = flag.String("laddr", "", "local address that STUN client will use")
	sendData       = flag.String("send", "", "data to be sent")
)

func main() {
	var wg sync.WaitGroup

	flag.Parse()

	if *stunServerMode {
		if server, err := NewStunServer(*remoteAddr); err == nil {
			wg.Add(1)
			go server.run(&wg)
		} else {
			log.Fatalln("Failed starting the STUN server: %v", err)
		}
		wg.Wait()
		log.Println("Server is exiting.")
	} else if *sendData != "" {
		c, err := stun.Dial("udp", *remoteAddr)
		if err != nil {
			log.Fatalln("Failed dialing to destination", *remoteAddr)
		}
		msg := stun.MustBuild(
			stun.TransactionID,
			stunDataRequest,
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
			raddr, laddr *net.UDPAddr
			overlay      *OverlayConn
			err          error
		)
		if raddr, err = net.ResolveUDPAddr("udp", *remoteAddr); err != nil {
			log.Fatalln("Cannot resolve STUN server address (raddr):", err)
		}
		if laddr, err = net.ResolveUDPAddr("udp", *localAddr); err != nil {
			log.Fatalln("Cannot resolve local address (laddr):", err)
		}
		if overlay, err = NewOverlayConn(raddr, laddr); err != nil {
			log.Fatalln("Cannot crete overlay:", err)
		}
		time.Sleep(10 * time.Minute)
		log.Println("overlay's state:", overlay.automata.current)
		overlay.Close()
		time.Sleep(5 * time.Second)
		log.Println("overlay's state:", overlay.automata.current)
	}
}
