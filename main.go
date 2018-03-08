// Copyright 2018 University of Glasgow.
// Use of this source code is governed by an Apache
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
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
			msg          []byte
			buf          [64 * 1024]byte
			err          error
		)
		if raddr, err = net.ResolveUDPAddr("udp", *remoteAddr); err != nil {
			log.Fatalln("Cannot resolve STUN server address (raddr):", err)
		}
		if laddr, err = net.ResolveUDPAddr("udp", *localAddr); err != nil {
			log.Fatalln("Cannot resolve local address (laddr):", err)
		}
		if overlay, err = NewOverlayConn(raddr, laddr, nil); err != nil {
			log.Fatalln("Cannot crete overlay:", err)
		}
		go func() {
			for {
				if n, err := overlay.Read(buf[:]); err != nil {
					log.Println("failed reading from overlay", err)
				} else {
					log.Printf("read a message from overlay: %s", string(buf[:n]))
				}
			}
		}()
		msg = []byte(fmt.Sprintf("message from %s", overlay.ID))
		for {
			if _, err = overlay.Write(msg); err != nil {
				log.Println("failed writing to overlay:", err)
			} else {
				log.Println("successfully wrote to overlay")
			}
			time.Sleep(time.Second)
		}
	}
}
