package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/anacrolix/torrent"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/spacemonkeygo/openssl"
	"github.com/zeebo/bencode"

	"github.com/valyala/fasthttp"
)

const (
	DefaultUnixAddress = "p2pupdate.sock"
)

type Agent struct {
	Config        AgentConfig
	Overlay       *OverlayConn
	PublicKey     openssl.PublicKey
	TorrentClient *torrent.Client
}

type AgentConfig struct {
	OverlayConfig OverlayConfig `json:"overlay,omitempty"`
	PublicKeyFile string        `json:"public-key-file,omitempty"`
	Api           struct {
		Address string `json:"address,omitempty"`
	} `json:"api,omitempty"`
	Data struct {
		Dir string `json:"dir,omitempty"`
	} `json:"data,omitempty"`
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

	// create Torrent Client
	torrentCfg := &torrent.Config{
		DataDir:       cfg.Data.Dir,
		Seed:          true,
		HTTPUserAgent: softwareName,
		//PeerID:        a.Overlay.ID.String(),
	}
	if a.TorrentClient, err = torrent.NewClient(torrentCfg); err != nil {
		return fmt.Errorf("ERROR: failed creating Torrent client: %v", err)
	}

	// start REST API service
	go a.startRestApi()

	// start Overlay network
	return a.startOverlay()
}

func (a *Agent) startOverlay() error {
	var (
		buf [64 * 1024]byte
		err error
	)
	if a.Overlay, err = NewOverlayConn(a.Config.OverlayConfig); err != nil {
		return err
	}
	for {
		if n, err := a.Overlay.Read(buf[:]); err != nil {
			log.Println("failed reading from overlay", err)
		} else {
			log.Printf("read a message from overlay: %s", string(buf[:n]))
		}
	}
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
		update Update
		err    error
	)

	if err = json.Unmarshal(ctx.PostBody(), &update); err != nil {
		log.Printf("failed to decode request update: %v", err)
		ctx.Response.SetStatusCode(400)
	} else if err = update.validate(&a.PublicKey); err != nil {
		log.Printf("torrent and update file do not match: %v", err)
		ctx.Response.SetStatusCode(406)
	} else if err = update.Write(a.Overlay); err != nil {
		log.Printf("failed to distribute the torrent-file: %v", err)
		ctx.Response.SetStatusCode(500)
	} else if err = update.start(a.TorrentClient); err != nil {
		log.Printf("failed to activating the torrent: %v", err)
		ctx.Response.SetStatusCode(503)
	} else {
		ctx.Response.SetStatusCode(200)
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

type Update struct {
	Metainfo Metainfo `json:"metainfo"`
	Filename string   `json:"filename"`

	torrent *torrent.Torrent `json:"-"`
}

func (u *Update) validate(key *openssl.PublicKey) error {
	if err := u.Metainfo.Verify(*key); err != nil {
		j, _ := json.Marshal(*u)
		log.Println(string(j))
		return fmt.Errorf("invalid torrent-file: %v", err)
	}
	info := metainfo.Info{
		PieceLength: u.Metainfo.InfoBytes.PieceLength,
	}
	if err := info.BuildFromFilePath(u.Filename); err != nil {
		return fmt.Errorf("ERROR: failed to generate piece-hashes from '%s': %v", u.Filename, err)
	}
	if bytes.Compare(info.Pieces, u.Metainfo.InfoBytes.Pieces) != 0 {
		return fmt.Errorf("ERROR: piece-hashes of '%s' and torrent-file do not match", u.Filename)
	}
	return nil
}

func (u *Update) Write(w io.Writer) error {
	var (
		b   []byte
		err error
	)

	if b, err = bencode.EncodeBytes(u.Metainfo); err != nil {
		return fmt.Errorf("failed to generating bencode from Metainfo: %v", err)
	}
	_, err = w.Write(b)
	return err
}

func (u *Update) start(c *torrent.Client) error {
	var (
		mi  *metainfo.MetaInfo
		err error
	)

	if mi, err = u.Metainfo.torrentMetainfo(); err != nil {
		return fmt.Errorf("failed generating torrent metainfo: %v", err)
	} else if u.torrent, err = c.AddTorrent(mi); err != nil {
		return fmt.Errorf("failed adding torrent: %v", err)
	}
	go func() {
		for {
			log.Println(u.String())
			time.Sleep(2 * time.Second)
		}
	}()
	return err
}

func (u *Update) stop(c *torrent.Client) error {
	// TODO
	return nil
}

func (u *Update) String() string {
	var b bytes.Buffer
	b.WriteString("uuid:")
	b.WriteString(u.Metainfo.UUID)
	b.WriteString(" version:")
	b.WriteString(u.Metainfo.Version)
	if u.torrent != nil {
		b.WriteString(fmt.Sprintf(" completed/missing:%v/%v",
			u.torrent.BytesCompleted(), u.torrent.BytesMissing()))
		stats := u.torrent.Stats()
		b.WriteString(
			fmt.Sprintf(" seeding:%v peers(total/active):%v/%v read/write:%v/%v",
				u.torrent.Seeding(), stats.TotalPeers, stats.ActivePeers,
				stats.BytesRead, stats.BytesWritten))
	}
	return b.String()
}
