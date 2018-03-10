// Copyright 2018 University of Glasgow.
// Use of this source code is governed by an Apache
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/gortc/stun"
	"gopkg.in/urfave/cli.v1"
)

func serverCmd(ctx *cli.Context) error {
	var (
		wg  sync.WaitGroup
		s   *StunServer
		cfg *ServerConfig
		err error
	)

	if cfg, err = NewServerConfigFromFile(ctx.GlobalString("config-file")); err != nil {
		return err
	}
	if s, err = NewStunServer(ctx.String("address"), *cfg); err != nil {
		return err
	}
	wg.Add(1)
	go s.run(&wg)
	wg.Wait()
	log.Println("Server is exiting.")
	return nil
}

func agentCmd(ctx *cli.Context) error {
	var (
		cfg     *OverlayConfig
		overlay *OverlayConn
		msg     []byte
		buf     [64 * 1024]byte
		err     error
	)
	if cfg, err = NewOverlayConfigFromFile(ctx.GlobalString("config-file")); err != nil {
		return err
	}
	if overlay, err = NewOverlayConn(ctx.String("server"), ctx.String("address"), *cfg); err != nil {
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

func sendCmd(ctx *cli.Context) error {
	var (
		addr = ctx.String("address")
		c    *stun.Client
		err  error
	)

	if c, err = stun.Dial("udp", addr); err != nil {
		return fmt.Errorf("failed dialing to %s", addr)
	}
	return c.Indicate(stun.MustBuild(
		stun.TransactionID,
		stunDataRequest,
		stun.NewUsername(ctx.App.Name),
		PeerMessage([]byte(ctx.String("message"))),
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	))
}

func main() {
	app := cli.NewApp()

	app.Usage = "Peer-to-peer secure update"
	app.Version = "0.0.1"
	app.EnableBashCompletion = true
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "config-file",
			Value: "",
			Usage: "Path of config file",
		},
	}
	app.Commands = []cli.Command{
		{
			Name:   "agent",
			Usage:  "agent mode",
			Action: agentCmd,
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "address",
					Value: "0.0.0.0:9322",
					Usage: "Address which the agent will listen to or send from",
				},
				cli.StringFlag{
					Name:  "server",
					Value: "fruit-testbed.org:3478",
					Usage: "Address of the server",
				},
			},
		},
		{
			Name:   "server",
			Usage:  "server mode",
			Action: serverCmd,
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "address",
					Value: "0.0.0.0:3478",
					Usage: "Address which the server will listen to",
				},
			},
		},
		{
			Name:   "send",
			Usage:  "send a STUN message to an agent/server",
			Action: sendCmd,
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "address",
					Usage: "Address to send",
				},
				cli.StringFlag{
					Name:  "message",
					Value: "Aloha!",
					Usage: "Message to be sent",
				},
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatalln(err)
	}
}
