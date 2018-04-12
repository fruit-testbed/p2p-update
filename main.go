// Copyright 2018 University of Glasgow.
// Use of this source code is governed by an Apache
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/valyala/fasthttp"
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

	mi, err := NewNotification(
		filename,
		uuid,
		ver,
		ctx.String("tracker"),
		ctx.Int64("piece-length"),
		key)
	if err != nil {
		return err
	}

	u := Update{
		Source:       filename,
		Notification: *mi,
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

	if serverAddr := ctx.String("server"); len(serverAddr) > 0 {
		if err = submitToServer(&u, serverAddr); err != nil {
			return err
		}
	}

	return submitToAgent(&u, ctx.String("unix-socket"))
}

func submitToServer(u *Update, addr string) error {
	req := fasthttp.AcquireRequest()
	req.SetRequestURI(fmt.Sprintf("http://%s", addr))
	req.Header.SetMethod("POST")
	if err := json.NewEncoder(req.BodyWriter()).Encode(u.Notification); err != nil {
		return fmt.Errorf("submitToServer - failed encoding notification: %v", err)
	}
	res := fasthttp.AcquireResponse()
	if err := fasthttp.DoDeadline(req, res, time.Now().Add(5*time.Second)); err != nil {
		return fmt.Errorf("submitToServer - failed http request: %v", err)
	}
	if res.StatusCode() != 200 {
		return fmt.Errorf("submitToServer - status code: %d", res.StatusCode())
	}
	return nil
}

func submitToAgent(u *Update, addr string) error {
	client := fasthttp.Client{
		Dial: func(_ string) (net.Conn, error) {
			return net.Dial("unix", addr)
		},
	}
	req := fasthttp.AcquireRequest()
	req.SetRequestURI(updateURL)
	req.Header.SetMethod("POST")
	if err := json.NewEncoder(req.BodyWriter()).Encode(u); err != nil {
		return fmt.Errorf("submitToAgent - failed encoding update: %v", err)
	}
	res := fasthttp.AcquireResponse()
	if err := client.DoDeadline(req, res, time.Now().Add(5*time.Second)); err != nil {
		return fmt.Errorf("submitToAgent - failed http request: %v", err)
	}
	if res.StatusCode() != 200 {
		return fmt.Errorf("submitToAgent - status code: %d", res.StatusCode())
	}
	return nil
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

	if cfg, err = NewConfig(ctx.String("config-file")); err != nil {
		return err
	} else if a, err = NewAgent(cfg); err != nil {
		return err
	}
	a.Wait()
	log.Println("Agent has stopped.")
	return nil
}

func main() {
	app := cli.NewApp()

	app.Usage = "Peer-to-peer secure update"
	app.Version = "0.0.1"
	app.EnableBashCompletion = true

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
					Usage: "Update file or directory",
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
					Usage: "output notification file, or - for STDOUT",
				},
				cli.StringFlag{
					Name:  "tracker, t",
					Value: DefaultTracker,
					Usage: "BitTorrent tracker address",
				},
				cli.Int64Flag{
					Name:  "piece-length, l",
					Value: DefaultPieceLength,
					Usage: "Piece length",
				},
				cli.StringFlag{
					Name:  "unix-socket, x",
					Value: "/var/run/p2pupdate.sock",
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
					Name:  "config-file, c",
					Value: "config.json",
					Usage: "Path of config file",
				},
			},
		},
		{
			Name:   "server",
			Usage:  "server mode",
			Action: serverCmd,
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "address, a",
					Value: ":3478",
					Usage: "Address which the server will listen to",
				},
				cli.IntFlag{
					Name:  "advertise-session, s",
					Value: 60,
					Usage: "Session table advertisement time (in second)",
				},
				cli.StringFlag{
					Name:  "database, d",
					Value: "server.db",
					Usage: "Server database file",
				},
				cli.IntFlag{
					Name:  "snapshot-time, n",
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
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatalln(err)
	}
}
