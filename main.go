// Copyright 2018 University of Glasgow.
// Use of this source code is governed by an Apache
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"time"

	"github.com/gortc/stun"
	"github.com/pkg/errors"
	"gopkg.in/urfave/cli.v1"
)

func submitCmd(ctx *cli.Context) error {
	filename, err := filepath.Abs(ctx.String("file"))
	if _, err := os.Stat(filename); err != nil {
		return fmt.Errorf("update file '%s' does not exist", filename)
	}

	uuid := ctx.String("uuid")
	if len(uuid) == 0 {
		return fmt.Errorf("UUID is empty")
	}

	ver := ctx.Uint64("version")
	if ver <= 0 {
		ver = uint64(time.Now().UTC().Unix())
	}

	key, err := LoadPrivateKey(ctx.String("private-key"))
	if err != nil {
		return errors.Wrap(err, "failed loading private key")
	}

	mi, err := NewMetainfo(
		filename,
		uuid,
		ver,
		ctx.String("tracker"),
		int64(ctx.Uint64("piece-length")),
		key)
	if err != nil {
		return err
	}

	u := Update{
		Source:   filename,
		Metainfo: *mi,
	}

	if output := ctx.String("output"); output != "" {
		w := os.Stdout
		if output != "-" {
			var err error
			w, err = os.OpenFile(output, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return err
			}
			defer w.Close()
		}
		return json.NewEncoder(w).Encode(&u)
	}

	if err = submitToServer(&u, ctx.String("server")); err != nil {
		return err
	}
	return submitToAgent(&u, ctx.String("unix-socket"))
}

func submitToServer(u *Update, addr string) error {
	buf := bytes.NewBufferString("")
	if err := json.NewEncoder(buf).Encode(u.Metainfo); err != nil {
		return errors.Wrap(err, "failed encoding update metainfo")
	}
	url := fmt.Sprintf("http://%s", addr)
	resp, err := http.DefaultClient.Post(url, "application/json", buf)
	if err == nil && resp.StatusCode != 200 {
		err = fmt.Errorf("status code: %d", resp.StatusCode)
	}
	return err
}

func submitToAgent(u *Update, addr string) error {
	buf := bytes.NewBufferString("")
	if err := json.NewEncoder(buf).Encode(u); err != nil {
		return err
	}
	httpc := http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", addr)
			},
		},
	}
	resp, err := httpc.Post("http://v1/update", "application/json", buf)
	if err == nil && resp.StatusCode != 200 {
		err = fmt.Errorf("status code: %d", resp.StatusCode)
	}
	return err
}

func serverCmd(ctx *cli.Context) error {
	var (
		wg  sync.WaitGroup
		s   *Server
		err error
	)

	cfg := DefaultServerConfig()
	if addr := ctx.String("address"); addr != "" {
		cfg.Address = addr
	}
	if t := ctx.Int("advertise-session"); t > 0 {
		cfg.SessionAdvertiseTime = t
	}
	if db := ctx.String("database"); db != "" {
		cfg.Database = db
	}
	if t := ctx.Int("snapshot-time"); t > 0 {
		cfg.SnapshotTime = t
	}
	if f := ctx.String("public-key"); f != "" {
		cfg.PublicKey.Filename = f
	}
	if s, err = NewServer(*cfg); err != nil {
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

	homeDir := "~/"
	if user, err := user.Current(); err == nil {
		homeDir = user.HomeDir
	}

	app.Commands = []cli.Command{
		{
			Name:   "submit",
			Usage:  "submit a new update",
			Action: submitCmd,
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "file, f",
					Usage: "Update file",
				},
				cli.Uint64Flag{
					Name:  "version, v",
					Usage: "Update version, 0 equals to current Unix timestamp",
				},
				cli.StringFlag{
					Name:  "uuid, u",
					Value: UUIDShell,
					Usage: "Target resource UUID",
				},
				cli.StringFlag{
					Name:  "private-key, k",
					Value: fmt.Sprintf("%s/.ssh/id_rsa", homeDir),
					Usage: "Private key for signing",
				},
				cli.StringFlag{
					Name:  "output, o",
					Usage: "output torrent file, or - for STDOUT",
				},
				cli.StringFlag{
					Name:  "tracker, t",
					Value: DefaultTracker,
					Usage: "BitTorrent tracker address",
				},
				cli.Uint64Flag{
					Name:  "piece-length, l",
					Value: DefaultPieceLength,
					Usage: "Piece length",
				},
				cli.StringFlag{
					Name:  "unix-socket, x",
					Value: "p2pupdate.sock",
					Usage: "Agent's unix socket file",
				},
				cli.StringFlag{
					Name:  "server, s",
					Value: "fruit-testbed.org:3478",
					Usage: "Server address",
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
					Value: ":3478",
					Usage: "Address which the server will listen to",
				},
				cli.IntFlag{
					Name:  "advertise-session",
					Value: 60,
					Usage: "Session table advertisement time (in second)",
				},
				cli.StringFlag{
					Name:  "database",
					Value: "server.db",
					Usage: "Server database file",
				},
				cli.IntFlag{
					Name:  "snapshot-time",
					Value: 10,
					Usage: "Snapshot database interval (in second)",
				},
				cli.StringFlag{
					Name:  "public-key, k",
					Value: "key.pub",
					Usage: "Public key for verification",
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
