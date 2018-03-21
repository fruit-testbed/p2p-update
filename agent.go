package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/anacrolix/dht"

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

// Agent is a representation of update agent.
type Agent struct {
	Config    *Config
	Overlay   *OverlayConn
	PublicKey *openssl.PublicKey
	Updates   map[string]*Update

	torrentClient *torrent.Client
	quit          chan interface{}
}

// Config specifies agent configurations.
type Config struct {
	// Public key file for verification
	PublicKeyFile string `json:"public-key-file,omitempty"`

	// Overlay network configurations for gossip protocol
	Overlay OverlayConfig `json:"overlay,omitempty"`

	// REST API configuration
	API struct {
		Address string `json:"address,omitempty"`
	} `json:"api,omitempty"`

	// BitTorrent client configurations
	BitTorrent struct {
		DataDir     string `json:"data-dir,omitempty"`
		Tracker     string `json:"tracker,omitempty"`
		Debug       bool   `json:"debug,omitempty"`
		Address     string `json:"address,omitempty"`
		PieceLength int64  `json:"piece-length,omitempty"`
	} `json:"bittorrent,omitempty"`
}

// NewConfig loads configurations from given file.
func NewConfig(filename string) (Config, error) {
	var (
		f   *os.File
		err error
	)

	cfg := Config{
		PublicKeyFile: "key.pub",
	}
	cfg.API.Address = "p2pupdate.sock"
	cfg.BitTorrent.DataDir = "data/"
	cfg.BitTorrent.Tracker = "http://0d.kebhana.mx:443/announce"
	cfg.BitTorrent.Debug = false
	cfg.BitTorrent.Address = ":50007"
	cfg.BitTorrent.PieceLength = 32 * 1024

	if f, err = os.Open(filename); err == nil {
		err = json.NewDecoder(f).Decode(&cfg)
	}
	return cfg, err
}

// NewAgent creates an Agent instance and immediately starts it.
func NewAgent(cfg Config) (*Agent, error) {
	var (
		b   []byte
		pub openssl.PublicKey
		err error
	)

	a := &Agent{
		Config:  &cfg,
		Updates: make(map[string]*Update),
		quit:    make(chan interface{}),
	}

	// start Overlay network
	if a.Overlay, err = NewOverlayConn(a.Config.Overlay); err != nil {
		return nil, err
	}

	// load public key file
	if b, err = ioutil.ReadFile(cfg.PublicKeyFile); err != nil {
		return nil, fmt.Errorf("ERROR: failed reading public key file '%s': %v", cfg.PublicKeyFile, err)
	}
	if pub, err = openssl.LoadPublicKeyFromPEM(b); err != nil {
		return nil, fmt.Errorf("ERROR: failed loading public key file '%s: %v", cfg.PublicKeyFile, err)
	}
	a.PublicKey = &pub

	// create Torrent Client
	torrentCfg := &torrent.Config{
		DataDir:       cfg.BitTorrent.DataDir,
		Seed:          true,
		HTTPUserAgent: softwareName,
		Debug:         cfg.BitTorrent.Debug,
		DHTConfig: dht.ServerConfig{
			StartingNodes: dht.GlobalBootstrapAddrs,
		},
	}
	if len(cfg.BitTorrent.Address) > 0 {
		torrentCfg.ListenAddr = cfg.BitTorrent.Address
	}
	if a.torrentClient, err = torrent.NewClient(torrentCfg); err != nil {
		return nil, fmt.Errorf("ERROR: failed creating Torrent client: %v", err)
	}
	log.Printf("Torrent Client listen at %v", a.torrentClient.ListenAddr())

	go a.startCatchingSignals()
	go a.startRestAPI()
	go a.startGossip()

	return a, nil
}

// Stop stops the agent.
func (a *Agent) Stop() {
	log.Println("cleaning up agent")
	if _, err := os.Stat(a.Config.API.Address); err == nil {
		os.Remove(a.Config.API.Address)
	}
	log.Println("cleaned up agent")
}

// Wait waits until the agent stopped.
func (a *Agent) Wait() {
	select {
	case <-a.quit:
	}
}

func (a *Agent) startCatchingSignals() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	for {
		switch <-c {
		// catch SIGINT & Ctrl-C signal, then do the cleanup
		case os.Interrupt, os.Kill:
			a.Stop()
			a.quit <- 1
		}
	}
}

func (a *Agent) startGossip() {
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
				if u, err = NewUpdate(b); err != nil {
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
	} else if err = update.Verify(a); err != nil {
		log.Printf("torrent and update file do not match: %v", err)
		ctx.Response.SetStatusCode(401)
	} else if err = update.start(a); err != nil {
		switch err {
		case errUpdateIsAlreadyExist:
			ctx.Response.SetStatusCode(208)
		case errUpdateIsOlder:
			ctx.Response.SetStatusCode(406)
		default:
			ctx.Response.SetStatusCode(500)
			log.Printf("failed to activating the torrent: %v", err)
		}
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

// Update represents a system update that should be downloaded and deployed on
// the system. It also has to be distributed to other peers.
type Update struct {
	sync.RWMutex

	Metainfo Metainfo `json:"metainfo"`
	Filename string   `json:"filename"`

	torrent *torrent.Torrent
	stopped bool
}

// NewUpdate creates an Update instance from given torrent-file++.
func NewUpdate(b []byte) (*Update, error) {
	u := Update{}
	err := bencode.DecodeBytes(b, &u.Metainfo)
	return &u, err
}

// Verify verifies the update. It returns an error if the verification fails,
// otherwise nil.
func (u *Update) Verify(a *Agent) error {
	return u.Metainfo.Verify(*a.PublicKey)
}

// start starts the lifecycle of an update.
func (u *Update) start(a *Agent) error {
	var (
		mi  *metainfo.MetaInfo
		err error
	)

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

	// activate torrent
	log.Printf("starting update: %s", u.String())
	if mi, err = u.Metainfo.torrentMetainfo(); err != nil {
		return fmt.Errorf("failed generating torrent metainfo: %v", err)
	}
	if u.torrent, err = a.torrentClient.AddTorrent(mi); err != nil {
		return fmt.Errorf("failed adding torrent: %v", err)
	}
	u.Lock()
	u.stopped = false
	u.Unlock()

	// spawn a go-routine that logs torrent's stats
	go func() {
		for {
			u.RLock()
			stopped := u.stopped
			u.RUnlock()
			if stopped {
				break
			}
			if u.torrent.BytesMissing() > 0 {
				<-u.torrent.GotInfo()
				u.torrent.DownloadAll()
			}
			log.Println(u.String())
			time.Sleep(5 * time.Second)
		}
	}()

	// re-distribute the update to peers
	if err = u.Metainfo.Write(a.Overlay); err != nil {
		log.Printf("WARNING: failed to multicast update:[%v]: %v", u.String(), err)
	}

	return nil
}

// stop stops the lifecycle of the update.
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
		s := u.torrent.PieceState(0)
		b.WriteString(
			fmt.Sprintf(" piece[0]checking:%v complete:%v ok:%v partial:%v priority:%v",
				s.Checking, s.Complete, s.Ok, s.Partial, s.Priority))
	}
	return b.String()
}

func (u *Update) deploy() error {
	// TODO
	return nil
}
