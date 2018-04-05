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
	"strconv"
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

// BitTorrentConfig holds configurations of BitTorrent client.
type BitTorrentConfig struct {
	MetadataDir string `json:"metadata-dir,omitempty"`
	DataDir     string `json:"data-dir,omitempty"`
	Tracker     string `json:"tracker,omitempty"`
	Debug       bool   `json:"debug,omitempty"`
	PieceLength int64  `json:"piece-length,omitempty"`
	Port        int    `json:"port,omitempty"`

	externalPort int
}

// APIConfig holds configurations of API service.
type APIConfig struct {
	Address string `json:"address,omitempty"`
}

// Key holds an encryption key file or the key (value) itself.
type Key struct {
	Filename string `json:"filename,omitempty"`
	Value    string `json:"value,omitempty"`
}

// Config specifies agent configurations.
type Config struct {
	Address string `json:"address,omitempty"`
	Server  string `json:"server,omitempty"`

	// Public key file for verification
	PublicKey Key `json:"public-key,omitempty"`

	// Proxy=true means the agent will not deploy the update
	// on local node
	Proxy bool `json:"proxy,omitempty"`

	// Overlay network configurations for gossip protocol
	Overlay OverlayConfig `json:"overlay,omitempty"`

	// REST API configuration
	API APIConfig `json:"api,omitempty"`

	// BitTorrent client configurations
	BitTorrent BitTorrentConfig `json:"bittorrent,omitempty"`
}

func (c *Config) torrentClientConfig() *torrent.Config {
	addr := strings.Trim(c.Address, " \t\n\r")
	overlayPort := 0
	if ok, err := regexp.MatchString(`^.*:[0-9]+$`, addr); ok && err == nil {
		i := strings.Index(addr, ":")
		overlayPort, _ = strconv.Atoi(addr[i+1:])
		addr = addr[0:i]
	}

	for c.BitTorrent.Port == 0 || c.BitTorrent.Port == overlayPort {
		c.BitTorrent.Port = bindRandomPort()
	}

	return &torrent.Config{
		ListenAddr:    fmt.Sprintf("%s:%d", addr, c.BitTorrent.Port),
		DataDir:       c.BitTorrent.DataDir,
		Seed:          true,
		NoDHT:         false,
		HTTPUserAgent: softwareName,
		Debug:         c.BitTorrent.Debug,
		DHTConfig: dht.ServerConfig{
			StartingNodes: dht.GlobalBootstrapAddrs,
		},
	}
}

func (c *Config) createDirs() error {
	if _, err := os.Stat(c.BitTorrent.DataDir); err != nil {
		if err = os.Mkdir(c.BitTorrent.DataDir, 0750); err != nil {
			return err
		}
	}
	if _, err := os.Stat(c.BitTorrent.MetadataDir); err != nil {
		if err = os.Mkdir(c.BitTorrent.MetadataDir, 0750); err != nil {
			return err
		}
	}
	return nil
}

// NewConfig loads configurations from given file.
func NewConfig(filename string) (Config, error) {
	var (
		f   *os.File
		err error
	)

	cfg := DefaultConfig()

	if f, err = os.Open(filename); err == nil {
		err = json.NewDecoder(f).Decode(&cfg)
	}

	return cfg, err
}

// DefaultConfig returns default agent configurations.
func DefaultConfig() Config {
	return Config{
		Server: "fruit-testbed.org:3478",
		PublicKey: Key{
			Filename: "key.pub",
		},
		Proxy: false,
		API: APIConfig{
			Address: "p2pupdate.sock",
		},
		BitTorrent: BitTorrentConfig{
			MetadataDir: "torrent/",
			DataDir:     "data/",
			Tracker:     DefaultTracker,
			Debug:       false,
			PieceLength: DefaultPieceLength,
		},
		Overlay: OverlayConfig{
			BindingWait:         30 * time.Second,
			BindingMaxErrors:    10,
			ListeningWait:       30 * time.Second,
			ListeningMaxErrors:  10,
			ListeningBufferSize: 64 * 1024,
			ErrorBackoff:        10 * time.Second,
			ChannelLifespan:     60 * time.Second,
		},
	}
}

// NewAgent creates an Agent instance and immediately starts it.
func NewAgent(cfg Config) (*Agent, error) {
	var (
		b   []byte
		pub openssl.PublicKey
		err error
	)

	j, _ := json.Marshal(cfg)
	log.Printf("creating agent with config: %s", string(j))

	a := &Agent{
		Config:  &cfg,
		Updates: make(map[string]*Update),
		quit:    make(chan interface{}),
	}

	// create required directories if necessary
	if err = cfg.createDirs(); err != nil {
		return nil, err
	}

	// create Torrent Client
	a.torrentClient, err = torrent.NewClient(cfg.torrentClientConfig())
	if err != nil {
		return nil, fmt.Errorf("ERROR: failed creating Torrent client: %v", err)
	}
	log.Printf("Torrent Client listen at %v", a.torrentClient.ListenAddr())

	// updated Overlay config
	a.Config.Overlay.Address = a.Config.Address
	a.Config.Overlay.Server = a.Config.Server
	a.Config.Overlay.torrentPorts = [2]int{a.Config.BitTorrent.Port, a.Config.BitTorrent.Port}

	// start Overlay network
	if a.Overlay, err = NewOverlayConn(a.Config.Overlay); err != nil {
		return nil, err
	}

	// load public key file
	if b, err = ioutil.ReadFile(cfg.PublicKey.Filename); err != nil {
		return nil, fmt.Errorf("ERROR: failed reading public key file '%s': %v", cfg.PublicKey.Filename, err)
	}
	if pub, err = openssl.LoadPublicKeyFromPEM(b); err != nil {
		return nil, fmt.Errorf("ERROR: failed loading public key file '%s: %v", cfg.PublicKey.Filename, err)
	}
	a.PublicKey = &pub

	// load update from local database
	a.loadUpdates()

	go a.startCatchingSignals()
	go a.startRestAPI()
	go a.startGossip()

	j, _ = json.Marshal(cfg)
	log.Printf("created agent with config: %s", string(j))

	return a, nil
}

// Stop stops the agent.
func (a *Agent) Stop() {
	if a.quit != nil {
		log.Println("cleaning up agent")
		if _, err := os.Stat(a.Config.API.Address); err == nil {
			os.Remove(a.Config.API.Address)
		}
		log.Println("cleaned up agent")
		a.quit <- 1
		a.quit = nil
	}
}

// Wait waits until the agent stopped.
func (a *Agent) Wait() {
	c := a.quit
	if c != nil {
		select {
		case <-c:
		}
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
		return rand.Intn(10000) + 50000
	}
	for _, d := range ds {
		for i := 0; i < 50; i++ {
			port := rand.Intn(10000) + 50000
			if _, err := d.AddPortMapping(nat.TCP, port, port, "anacrolix/torrent", 0); err == nil {
				return port
			}
		}
	}
	return rand.Intn(10000) + 50000
}
