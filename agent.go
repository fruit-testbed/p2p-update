package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/anacrolix/dht"
	"github.com/anacrolix/torrent"
	"github.com/pkg/errors"
	"github.com/spacemonkeygo/openssl"
	"github.com/syncthing/syncthing/lib/nat"
	"github.com/syncthing/syncthing/lib/upnp"
	"github.com/valyala/fasthttp"
)

var (
	errUpdateIsAlreadyExist     = errors.New("update is already exist")
	errUpdateIsOlder            = errors.New("update is older")
	errUpdateVerificationFailed = errors.New("update verification failed")
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

	// Proxy=true means the agent will not deploy the update
	// on local node
	Proxy bool `json:"proxy,omitempty"`

	// Overlay network configurations for gossip protocol
	Overlay OverlayConfig `json:"overlay,omitempty"`

	// REST API configuration
	API struct {
		Address string `json:"address,omitempty"`
	} `json:"api,omitempty"`

	// BitTorrent client configurations
	BitTorrent struct {
		MetadataDir string `json:"metadata-dir,omitempty"`
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
		Proxy:         false,
	}
	oc := &cfg.Overlay
	oc.SetDefault()
	cfg.API.Address = "p2pupdate.sock"
	cfg.BitTorrent.MetadataDir = "torrent/"
	cfg.BitTorrent.DataDir = "data/"
	cfg.BitTorrent.Tracker = DefaultTracker
	cfg.BitTorrent.Debug = false
	//cfg.BitTorrent.Address = ":50007"
	cfg.BitTorrent.PieceLength = DefaultPieceLength

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

	if _, err := os.Stat(cfg.BitTorrent.DataDir); err != nil {
		os.Mkdir(cfg.BitTorrent.DataDir, 0750)
	}
	if _, err := os.Stat(cfg.BitTorrent.MetadataDir); err != nil {
		os.Mkdir(cfg.BitTorrent.MetadataDir, 0750)
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

	cfg.BitTorrent.Address = strings.Trim(cfg.BitTorrent.Address, " \t\n\r")
	if ok, err := regexp.MatchString(`^.*:[0-9]+$`, cfg.BitTorrent.Address); ok && err == nil {
		torrentCfg.ListenAddr = cfg.BitTorrent.Address
	} else {
		torrentCfg.ListenAddr = fmt.Sprintf("%s:%d", cfg.BitTorrent.Address, bindRandomPort())
	}

	if a.torrentClient, err = torrent.NewClient(torrentCfg); err != nil {
		return nil, fmt.Errorf("ERROR: failed creating Torrent client: %v", err)
	}
	log.Printf("Torrent Client listen at %v", a.torrentClient.ListenAddr())

	if json, err := json.Marshal(a.Config); err == nil {
		log.Printf("config: %s", string(json))
	}

	a.loadUpdates()

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
				if u, err = NewUpdateFromMessage(b, a); err != nil {
					log.Printf("the gossip message is not an update: %v", err)
				} else if err = u.Start(a); err != nil {
					switch err {
					case errUpdateIsAlreadyExist, errUpdateIsOlder, errUpdateVerificationFailed:
						log.Printf("ignored the update: %v", err)
					default:
						log.Printf("failed adding the torrent-file++ to TorrentClient: %v", err)
					}
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

// loadUpdates loads existing updates from local database (or files).
func (a *Agent) loadUpdates() {
	log.Println("Loading updates from local database")

	_, err := os.Stat(a.Config.BitTorrent.MetadataDir)
	if err != nil {
		if os.Mkdir(a.Config.BitTorrent.MetadataDir, 0700) != nil {
			log.Fatalf("cannot create metadata dir: %s", a.Config.BitTorrent.MetadataDir)
		}
	}

	files, err := ioutil.ReadDir(a.Config.BitTorrent.MetadataDir)
	if err != nil {
		log.Fatalf("cannot read metadata dir: %s", a.Config.BitTorrent.MetadataDir)
	}
	for _, f := range files {
		filename := filepath.Join(a.Config.BitTorrent.MetadataDir, f.Name())
		u, err := LoadUpdateFromFile(filename, a)
		if err != nil {
			log.Printf("failed loading update metadata file %s: %v", f.Name(), err)
			continue
		}
		u.Start(a)
	}
	log.Printf("Loaded %d updates", len(a.Updates))
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
		u   Update
		err error
	)

	if err = json.Unmarshal(ctx.PostBody(), &u); err != nil {
		log.Printf("failed to decode request update: %v", err)
		ctx.Response.SetStatusCode(400)
		return
	}
	u.agent = a

	if _, err = os.Stat(u.Source); err == nil {
		dest := filepath.Join(a.Config.BitTorrent.DataDir, u.Metainfo.Info.Name)
		cmd := exec.Command("cp", "-af", u.Source, dest)
		if err := cmd.Run(); err != nil {
			log.Printf("failed copying update file from '%s' to '%s': %v",
				u.Source, dest, err)
			ctx.Response.SetStatusCode(403)
			return
		}
	} else {
		log.Printf("source file '%s' does not exist", u.Source)
		ctx.Response.SetStatusCode(404)
		return
	}

	if err = u.Start(a); err != nil {
		switch err {
		case errUpdateIsAlreadyExist:
			ctx.Response.SetStatusCode(208)
		case errUpdateVerificationFailed:
			ctx.Response.SetStatusCode(401)
		case errUpdateIsOlder:
			ctx.Response.SetStatusCode(406)
		default:
			ctx.Response.SetStatusCode(500)
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

func bindRandomPort() int {
	ds := upnp.Discover(0, 2*time.Second)
	if len(ds) == 0 {
		return -1
	}
	for _, d := range ds {
		for i := 0; i < 50; i++ {
			port := rand.Intn(10000) + 50000
			if _, err := d.AddPortMapping(nat.TCP, port, port, "anacrolix/torrent", 0); err == nil {
				return port
			}
		}
	}
	return 0
}
