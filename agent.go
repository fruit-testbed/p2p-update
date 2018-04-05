package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/dht"
	"github.com/anacrolix/torrent"
	"github.com/pkg/errors"
	"github.com/spacemonkeygo/openssl"
	"github.com/syncthing/syncthing/lib/nat"
	"github.com/syncthing/syncthing/lib/upnp"
)

var (
	errUpdateIsAlreadyExist     = errors.New("update is already exist")
	errUpdateIsOlder            = errors.New("update is older")
	errUpdateVerificationFailed = errors.New("update verification failed")
)

// Agent is a representation of update agent.
type Agent struct {
	sync.RWMutex

	Config    *Config
	Overlay   *OverlayConn
	PublicKey *openssl.PublicKey

	updates       map[string]*Update
	api           API
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
		updates: make(map[string]*Update),
		quit:    make(chan interface{}),
	}
	a.api.agent = a

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
	go a.api.Start()
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
	log.Printf("Loaded %d updates", len(a.updates))
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

func (a *Agent) addUpdate(u *Update) error {
	a.Lock()
	defer a.Unlock()
	uuid := u.Metainfo.UUID
	if _, ok := a.updates[uuid]; ok {
		return fmt.Errorf("an update with uuid:%s is already exist", uuid)
	}
	a.updates[uuid] = u
	return nil
}

func (a *Agent) deleteUpdate(uuid string) *Update {
	a.Lock()
	defer a.Unlock()
	u, ok := a.updates[uuid]
	delete(a.updates, uuid)
	if ok {
		return u
	}
	return nil
}

func (a *Agent) getUpdate(uuid string) *Update {
	a.RLock()
	defer a.RUnlock()
	if u, ok := a.updates[uuid]; ok {
		return u
	}
	return nil
}

func (a *Agent) getUpdateUUIDs() []string {
	a.RLock()
	defer a.RUnlock()
	keys := make([]string, 0, len(a.updates))
	for k := range a.updates {
		keys = append(keys, k)
	}
	return keys
}
