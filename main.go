// Copyright 2018 University of Glasgow.
// Use of this source code is governed by an Apache
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"sync"

	"github.com/gortc/stun"
	"github.com/spacemonkeygo/openssl"
	"gopkg.in/urfave/cli.v1"
)

func adminCmd(ctx *cli.Context) error {
	var (
		mi      *Metainfo
		content []byte
		key     openssl.PrivateKey
		cfg     Config
		err     error
	)

	if content, err = ioutil.ReadFile(ctx.String("private-key")); err != nil {
		return err
	}
	if key, err = openssl.LoadPrivateKeyFromPEM(content); err != nil {
		return err
	}

	cfg, err = NewConfig(ctx.GlobalString("config-file"))
	if err != nil {
		return err
	}

	mi, err = NewMetainfo(
		ctx.String("file"),
		ctx.String("uuid"),
		ctx.Int("version"),
		cfg.BitTorrent.Tracker,
		cfg.BitTorrent.PieceLength,
		&key)
	if err != nil {
		return err
	}

	w := os.Stdout
	filename := ctx.String("output")
	if filename != "" {
		f, err := os.OpenFile(filename,
			os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	if !ctx.Bool("json") {
		return mi.Write(w)
	}
	return json.NewEncoder(w).Encode(mi)
}

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
		a   *Agent
		cfg Config
		err error
	)

	if cfg, err = NewConfig(ctx.GlobalString("config-file")); err != nil {
		return err
	} else if a, err = NewAgent(cfg); err != nil {
		return err
	}
	a.Wait()
	log.Println("agent has been shutdown")
	return nil
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
			Value: "config.json",
			Usage: "Path of config file",
		},
	}
	app.Commands = []cli.Command{
		{
			Name:   "admin",
			Usage:  "admin mode",
			Action: adminCmd,
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "file",
					Usage: "Update file",
				},
				cli.IntFlag{
					Name:  "version",
					Usage: "Update version",
				},
				cli.StringFlag{
					Name:  "uuid",
					Usage: "Target resource UUID",
				},
				cli.StringFlag{
					Name:  "private-key",
					Usage: "Private key for signing",
				},
				cli.StringFlag{
					Name:  "output",
					Usage: "output torrent file",
				},
				cli.BoolFlag{
					Name:  "json",
					Usage: "use JSON instead of Bencode",
				},
			},
		},
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
