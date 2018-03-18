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
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/pkg/errors"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/spacemonkeygo/openssl"
	"github.com/zeebo/bencode"

	"github.com/valyala/fasthttp"
)

var (
	errUpdateIsAlreadyExist = errors.New("update is already exist")
	errUpdateIsOlder        = errors.New("update is older")
)

type Agent struct {
	Config        AgentConfig
	Overlay       *OverlayConn
	PublicKey     openssl.PublicKey
	TorrentClient *torrent.Client
	Updates       map[string]*Update
}

type AgentConfig struct {
	OverlayConfig OverlayConfig `json:"overlay,omitempty"`
	PublicKeyFile string        `json:"public-key-file,omitempty"`

	API struct {
		Address string `json:"address,omitempty"`
	} `json:"api,omitempty"`

	BitTorrent struct {
		DataDir string `json:"data-dir,omitempty"`
		DHT     bool   `json:"dht,omitempty"`
	} `json:"bittorrent,omitempty"`
}

func (a Agent) Start(cfg AgentConfig) error {
	var (
		b   []byte
		err error
	)

	a.Config = cfg
	a.Updates = make(map[string]*Update)

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

	// start Overlay network
	if a.Overlay, err = NewOverlayConn(a.Config.OverlayConfig); err != nil {
		return err
	}

	// load public key file
	if b, err = ioutil.ReadFile(cfg.PublicKeyFile); err != nil {
		return fmt.Errorf("ERROR: failed reading public key file '%s': %v", cfg.PublicKeyFile, err)
	}
	if a.PublicKey, err = openssl.LoadPublicKeyFromPEM(b); err != nil {
		return fmt.Errorf("ERROR: failed loading public key file '%s: %v", cfg.PublicKeyFile, err)
	}

	// create Torrent Client
	torrentCfg := &torrent.Config{
		DataDir:       cfg.BitTorrent.DataDir,
		Seed:          true,
		HTTPUserAgent: softwareName,
		NoDHT:         !cfg.BitTorrent.DHT,
		//PeerID:        a.Overlay.ID.String(),
	}
	if a.TorrentClient, err = torrent.NewClient(torrentCfg); err != nil {
		return fmt.Errorf("ERROR: failed creating Torrent client: %v", err)
	}

	// start REST API service
	go a.startRestAPI()

	// start listening gossip message
	a.listenGossip()
	return nil
}

func (a *Agent) listenGossip() {
	var (
		n   int
		buf [64 * 1024]byte
		u   *Update
		err error
	)

	for {
		if a != nil {
			if n, err = a.Overlay.Read(buf[:]); err != nil {
				log.Println("failed reading from overlay", err)
			} else {
				b := buf[:n]
				log.Printf("read a message from overlay: %s", string(b))
				if u, err = NewUpdateFromGossip(b); err != nil {
					log.Printf("the gossip message is not an update: %v", err)
				} else if err = u.start(a); err != nil {
					switch err {
					case errUpdateIsAlreadyExist, errUpdateIsOlder:
						log.Printf("ignored the update: %v", err)
					default:
						log.Printf("failed adding the torrent-file++ to TorrentClient: %v", err)
					}
				} else {
					log.Printf("INFO: New update has been started: %v", u.String())
				}
			}
		} else {
			log.Println("WARNING: No overlay is available!")
			time.Sleep(5 * time.Second)
		}
	}
}

func (a *Agent) cleanup() {
	if _, err := os.Stat(a.Config.API.Address); err == nil {
		os.Remove(a.Config.API.Address)
	}
}

func (a *Agent) startRestAPI() {
	if err := fasthttp.ListenAndServeUNIX(a.Config.API.Address, 0600, a.restRequestHandler); err != nil {
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
		ctx.Response.SetStatusCode(401)
	} else if err = update.Write(a.Overlay); err != nil {
		log.Printf("failed to distribute the torrent-file: %v", err)
		ctx.Response.SetStatusCode(500)
	} else if err = update.start(a); err != nil {
		switch err {
		case errUpdateIsAlreadyExist:
			ctx.Response.SetStatusCode(208)
		case errUpdateIsOlder:
			ctx.Response.SetStatusCode(406)
		default:
			ctx.Response.SetStatusCode(503)
		}
		log.Printf("failed to activating the torrent: %v", err)
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
	sync.RWMutex

	Metainfo Metainfo `json:"metainfo"`
	Filename string   `json:"filename"`

	torrent *torrent.Torrent
	stopped bool
}

func NewUpdateFromGossip(b []byte) (*Update, error) {
	u := Update{}
	err := bencode.DecodeBytes(b, u.Metainfo)
	return &u, err
}

func (u *Update) validate(key *openssl.PublicKey) error {
	if err := u.Metainfo.Verify(*key); err != nil {
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

func (u *Update) start(a *Agent) error {
	var (
		mi  *metainfo.MetaInfo
		err error
	)

	log.Printf("starting update: %s", u.String())

	if mi, err = u.Metainfo.torrentMetainfo(); err != nil {
		return fmt.Errorf("failed generating torrent metainfo: %v", err)
	}

	// Remove existing update that has the same UUID. If the existing update
	// is newer, then return an error.
	if cu, ok := a.Updates[u.Metainfo.UUID]; ok {
		if cu.Metainfo.Version > u.Metainfo.Version {
			return errUpdateIsOlder
		} else if cu.Metainfo.Version == u.Metainfo.Version {
			return errUpdateIsAlreadyExist
		}
		cu.stop()
	} else {
		log.Printf("existing update of uuid:%s does not exist", u.Metainfo.UUID)
	}
	a.Updates[u.Metainfo.UUID] = u

	if u.torrent, err = a.TorrentClient.AddTorrent(mi); err != nil {
		return fmt.Errorf("failed adding torrent: %v", err)
	}

	u.Lock()
	u.stopped = false
	u.Unlock()
	go func() {
		for {
			u.RLock()
			stopped := u.stopped
			u.RUnlock()
			if stopped {
				break
			}
			log.Println(u.String())
			time.Sleep(5 * time.Second)
		}
	}()

	return err
}

func (u *Update) stop() {
	if u.torrent != nil {
		log.Printf("stopping torrent: %v", u.String())
		u.torrent.Drop()
		<-u.torrent.Closed()
		u.Lock()
		u.stopped = true
		u.Unlock()
		log.Printf("closed torrent: %v", u.String())
	}
}

func (u *Update) String() string {
	var b bytes.Buffer
	b.WriteString(fmt.Sprintf("uuid:%v version:%d", u.Metainfo.UUID, u.Metainfo.Version))
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
