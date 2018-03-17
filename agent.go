package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/spacemonkeygo/openssl"
	"github.com/zeebo/bencode"

	"github.com/valyala/fasthttp"
)

const (
	DefaultUnixAddress = "p2pupdate.sock"
)

type Agent struct {
	Config    AgentConfig
	Overlay   *OverlayConn
	PublicKey openssl.PublicKey
}

type AgentConfig struct {
	OverlayConfig OverlayConfig `json:"overlay,omitempty"`
	PublicKeyFile string        `json:"public-key-file,omitempty"`
	Api           struct {
		Address string `json:"address,omitempty"`
	} `json:"api,omitempty"`
}

func (a *Agent) Start(cfg AgentConfig) error {
	var (
		b   []byte
		err error
	)

	a.Config = cfg

	// catch SIGINT signal, then do the cleanup
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		for {
			switch <-c {
			case os.Interrupt, os.Kill:
				a.cleanup()
				os.Exit(0)
			}
		}
	}()

	// load public key file
	if b, err = ioutil.ReadFile(cfg.PublicKeyFile); err != nil {
		return fmt.Errorf("ERROR: failed reading public key file '%s': %v", cfg.PublicKeyFile, err)
	}
	if a.PublicKey, err = openssl.LoadPublicKeyFromPEM(b); err != nil {
		return fmt.Errorf("ERROR: failed loading public key file '%s: %v", cfg.PublicKeyFile, err)
	}

	// start REST API service
	go a.startRestApi()

	// start Overlay network
	return a.startOverlay()
}

func (a *Agent) startOverlay() error {
	var (
		//msg []byte
		buf [64 * 1024]byte
		err error
	)
	if a.Overlay, err = NewOverlayConn(a.Config.OverlayConfig); err != nil {
		return err
	}
	//go func() {
	for {
		if n, err := a.Overlay.Read(buf[:]); err != nil {
			log.Println("failed reading from overlay", err)
		} else {
			log.Printf("read a message from overlay: %s", string(buf[:n]))
		}
	}
	//}()
	/*msg = []byte(fmt.Sprintf("message from %s", a.Overlay.ID))
	for {
		if _, err = a.Overlay.Write(msg); err != nil {
			log.Println("failed writing to overlay:", err)
		} else {
			log.Println("successfully wrote to overlay")
		}
		time.Sleep(time.Second)
	}*/
}

func (a *Agent) cleanup() {
	if _, err := os.Stat(a.Config.Api.Address); err == nil {
		os.Remove(a.Config.Api.Address)
	}
}

func (a *Agent) startRestApi() {
	if a.Config.Api.Address == "" {
		log.Printf("Using default unix address %s", DefaultUnixAddress)
		a.Config.Api.Address = DefaultUnixAddress
	}
	if err := fasthttp.ListenAndServeUNIX(a.Config.Api.Address, 0600, a.restRequestHandler); err != nil {
		log.Fatalf("Error in startRestApi: %v", err)
	}
}

func (a *Agent) restRequestHandler(ctx *fasthttp.RequestCtx) {
	if string(ctx.Host()) != "v1" {
		ctx.Response.SetStatusCode(400)
		return
	}
	switch string(ctx.Path()) {
	case "/overlay/peers":
		a.restRequestOverlayPeers(ctx)
	case "/update":
		a.restRequestUpdate(ctx)
	default:
		ctx.Response.SetStatusCode(400)
	}
}

type UpdatePostBody struct {
	Torrent  Metainfo
	Filename string
}

func (b *UpdatePostBody) validate() error {
	info := metainfo.Info{
		PieceLength: b.Torrent.InfoBytes.PieceLength,
	}
	if err := info.BuildFromFilePath(b.Filename); err != nil {
		return fmt.Errorf("ERROR: failed to generate piece-hashes from '%s': %v", b.Filename, err)
	}
	if bytes.Compare(info.Pieces, b.Torrent.InfoBytes.Pieces) != 0 {
		return fmt.Errorf("ERROR: piece-hashes of '%s' and torrent-file do not match", b.Filename)
	}
	return nil
}

func (a *Agent) restRequestUpdate(ctx *fasthttp.RequestCtx) {
	switch string(ctx.Method()) {
	case "POST":
		a.restRequestPostUpdate(ctx)
	default:
		ctx.Response.SetStatusCode(400)
	}
}

func (a *Agent) restRequestPostUpdate(ctx *fasthttp.RequestCtx) {
	var (
		body UpdatePostBody
		b    []byte
		err  error
	)

	if err = json.Unmarshal(ctx.PostBody(), &body); err != nil {
		log.Printf("failed to decode request body: %v", err)
		ctx.Response.SetStatusCode(400)
	} else if err = body.Torrent.Verify(a.PublicKey); err != nil {
		log.Printf("invalid torrent-file: %v", err)
		ctx.Response.SetStatusCode(406)
	} else if err = body.validate(); err != nil {
		log.Printf("torrent and update file do not match: %v", err)
		ctx.Response.SetStatusCode(406)
	} else {
		if b, err = bencode.EncodeBytes(body.Torrent); err != nil {
			log.Printf("failed to generating bencode from torrent-file: %v", err)
			ctx.Response.SetStatusCode(500)
		} else if _, err = a.Overlay.Write(b); err != nil {
			log.Printf("failed to distribute the torrent-file: %v", err)
			ctx.Response.SetStatusCode(500)
		} else {
			ctx.Response.SetStatusCode(200)
		}
	}
}

func (a *Agent) restRequestOverlayPeers(ctx *fasthttp.RequestCtx) {
	switch string(ctx.Method()) {
	case "GET":
		ctx.Response.Header.Set("Content-Type", "application/json")
		ctx.Response.SetBody(a.Overlay.peers.JSON())
	default:
		ctx.Response.SetStatusCode(400)
	}
}
