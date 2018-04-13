package main

import (
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"os/user"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/dht"
	"github.com/anacrolix/torrent"
	"github.com/pkg/errors"
	"github.com/syncthing/syncthing/lib/nat"
	"github.com/syncthing/syncthing/lib/upnp"
	"github.com/valyala/fasthttp"
	"github.com/zeebo/bencode"
	lumberjack "gopkg.in/natefinch/lumberjack.v2"
)

var (
	errUpdateIsAlreadyExist     = errors.New("update is already exist")
	errUpdateIsOlder            = errors.New("update is older")
	errUpdateVerificationFailed = errors.New("update verification failed")

	readBuffer       [64 * 1024]byte
	bufNotification  Notification
	bufNotifications = make(map[string]*Notification)
)

// Agent is a representation of update agent.
type Agent struct {
	sync.RWMutex

	Config    *Config
	Overlay   *OverlayConn
	PublicKey *rsa.PublicKey

	updates       map[string]*Update
	api           API
	torrentClient *torrent.Client
	quit          chan interface{}

	dataDir     string
	metadataDir string
}

// BitTorrentConfig holds configurations of BitTorrent client.
type BitTorrentConfig struct {
	Tracker     string `json:"tracker"`
	Debug       bool   `json:"debug"`
	PieceLength int64  `json:"piece-length"`
	Port        int    `json:"port"`
	NoDHT       bool   `json:"no-dht"`

	externalPort int
}

// APIConfig holds configurations of API service.
type APIConfig struct {
	Address string `json:"address"`
}

// Key holds an encryption key file or the key (value) itself.
type Key struct {
	Filename string `json:"filename"`
	Value    string `json:"value,omitempty"`
}

// Config specifies agent configurations.
type Config struct {
	Interface string `json:"interface"`
	Address   string `json:"address"`
	Server    string `json:"server"`
	DataDir   string `json:"data-dir"`
	LogFile   string `json:"log-file"`

	// Public key file for verification
	PublicKey Key `json:"public-key"`

	// Proxy=true means the agent will not deploy the update
	// on local node
	Proxy bool `json:"proxy"`

	// Overlay network configurations for gossip protocol
	Overlay OverlayConfig `json:"overlay"`

	// REST API configuration
	API APIConfig `json:"api"`

	// BitTorrent client configurations
	BitTorrent BitTorrentConfig `json:"bittorrent"`
}

func (a *Agent) torrentClientConfig() *torrent.Config {
	addr := strings.Trim(a.Config.Address, " \t\n\r")
	overlayPort := 0
	if ok, err := regexp.MatchString(`^.*:[0-9]+$`, addr); ok && err == nil {
		i := strings.Index(addr, ":")
		overlayPort, _ = strconv.Atoi(addr[i+1:])
		addr = addr[0:i]
	}

	if a.Config.BitTorrent.Port == 0 || a.Config.BitTorrent.Port == overlayPort {
		a.Config.BitTorrent.Port = bindRandomPort()
	}

	return &torrent.Config{
		ListenPort:       a.Config.BitTorrent.Port,
		DataDir:          a.dataDir,
		Seed:             true,
		NoDHT:            a.Config.BitTorrent.NoDHT,
		HTTPUserAgent:    softwareName,
		Debug:            a.Config.BitTorrent.Debug,
		DhtStartingNodes: dht.GlobalBootstrapAddrs,
	}
}

func (a *Agent) createDirs() error {
	a.dataDir = path.Join(a.Config.DataDir, "update")
	if err := os.MkdirAll(a.dataDir, 0750); err != nil {
		return errors.Wrapf(err, "createDirs - failed creating directory %s", a.dataDir)
	}
	a.metadataDir = path.Join(a.Config.DataDir, "notification")
	if err := os.MkdirAll(a.metadataDir, 0750); err != nil {
		return errors.Wrapf(err, "createDirs - failed creating directory %s", a.metadataDir)
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
	homeDir := "~/"
	if user, err := user.Current(); err == nil {
		homeDir = user.HomeDir
	}

	return Config{
		Server:  defaultServerAddress,
		DataDir: "/var/lib/p2pupdate",
		LogFile: "/var/log/p2pupdate.log",
		PublicKey: Key{
			Filename: fmt.Sprintf("%s/.ssh/id_rsa.pub", homeDir),
		},
		API: APIConfig{
			Address: defaultUnixSocket,
		},
		BitTorrent: BitTorrentConfig{
			Tracker:     DefaultTracker,
			PieceLength: DefaultPieceLength,
		},
		Overlay: OverlayConfig{
			StunPassword:        defaultStunPassword,
			BindingWait:         10,
			BindingMaxErrors:    5,
			ListeningWait:       30,
			ListeningMaxErrors:  10,
			ListeningBufferSize: 64 * 1024,
			ErrorBackoff:        10,
			ChannelLifespan:     60,
		},
	}
}

// NewAgent creates an Agent instance and immediately starts it.
func NewAgent(cfg Config) (*Agent, error) {
	if len(cfg.LogFile) > 0 {
		log.SetOutput(&lumberjack.Logger{
			Filename:   cfg.LogFile,
			MaxSize:    10,
			MaxBackups: 1,
			MaxAge:     28,
			Compress:   true,
		})
	}

	var err error

	j, _ := json.Marshal(cfg)
	log.Printf("creating agent with config: %s", string(j))

	a := &Agent{
		Config:  &cfg,
		updates: make(map[string]*Update),
		quit:    make(chan interface{}),
	}
	a.api.agent = a

	// create required directories if necessary
	if err = a.createDirs(); err != nil {
		return nil, err
	}

	// use the address of interface if it's given
	if len(a.Config.Address) == 0 && len(a.Config.Interface) > 0 {
		if ip := IPv4ofInterface(a.Config.Interface); ip != nil {
			a.Config.Address = fmt.Sprintf("%s:", ip.String())
			log.Printf("set agent address to %s from interface %s",
				a.Config.Address, a.Config.Interface)
		}
	}

	// create Torrent Client
	a.torrentClient, err = torrent.NewClient(a.torrentClientConfig())
	if err != nil {
		return nil, fmt.Errorf("ERROR: failed creating Torrent client: %v", err)
	}
	log.Printf("Torrent Client listen at %v", a.torrentClient.ListenAddrs())

	// updated Overlay config
	a.Config.Overlay.Address = a.Config.Address
	a.Config.Overlay.Server = a.Config.Server
	a.Config.Overlay.torrentPorts = [2]int{a.Config.BitTorrent.Port, a.Config.BitTorrent.Port}

	// start Overlay network
	if a.Overlay, err = NewOverlayConn(a.Config.Overlay); err != nil {
		return nil, err
	}

	// load public key file
	if a.PublicKey, err = LoadPublicKey(cfg.PublicKey.Filename); err != nil {
		return nil, fmt.Errorf("ERROR: failed loading public key file '%s: %v", cfg.PublicKey.Filename, err)
	}

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
	counter := 0
	for {
		if a == nil || !a.Overlay.Ready() {
			counter++
			time.Sleep(time.Second)
			if counter > 300 {
				counter = 0
				a.readTCP()
			}
		} else {
			counter = 0
			a.readOverlay()
		}
	}
}

func (a *Agent) readTCP() error {
	log.Println("readTCP - starting")
	url := fmt.Sprintf("http://%s", a.Config.Server)
	code, body, err := fasthttp.Get(nil, url)
	if code != 200 || err != nil {
		err := errors.Errorf("readTCP - failed getting updates from %s, status code: %d, error: %v", url, code, err)
		log.Println(err)
		return err
	}
	if err := json.Unmarshal(body, &bufNotifications); err != nil {
		err := errors.Errorf("readTCP - failed decoding notifications from %s, body: %s, : %v", url, string(body), err)
		log.Println(err)
		return err
	}
	for _, notification := range bufNotifications {
		u := NewUpdate(*notification, a)
		if err := u.Start(a); err != nil {
			switch err {
			case errUpdateIsAlreadyExist, errUpdateIsOlder, errUpdateVerificationFailed:
				log.Printf("readTCP - ignored the update: %v", err)
			default:
				log.Printf("readTCP - failed adding the torrent-file++ to TorrentClient: %v", err)
			}
		}
	}
	log.Println("readTCP - finished")
	return nil
}

func (a *Agent) readOverlay() {
	log.Println("readOverlay - starting")
	if n, err := a.Overlay.Read(readBuffer[:]); err != nil {
		log.Println("readOverlay - failed reading", err)
	} else {
		if err := bencode.DecodeBytes(readBuffer[:n], &bufNotification); err != nil {
			log.Printf("readOverlay - the gossip message is not a notification: %v", err)
		}
		if err = NewUpdate(bufNotification, a).Start(a); err != nil {
			switch err {
			case errUpdateIsAlreadyExist, errUpdateIsOlder, errUpdateVerificationFailed:
				log.Printf("readOverlay - ignored the update: %v", err)
			default:
				log.Printf("readOverlay - failed adding the torrent-file++ to TorrentClient: %v", err)
			}
		}
	}
	log.Println("readOverlay - finished")
}

// loadUpdates loads existing updates from local database (or files).
func (a *Agent) loadUpdates() {
	log.Println("Loading updates from local database")

	files, err := ioutil.ReadDir(a.metadataDir)
	if err != nil {
		log.Fatalf("cannot read metadata dir: %s", a.metadataDir)
	}
	for _, f := range files {
		filename := filepath.Join(a.metadataDir, f.Name())
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

func (a *Agent) addUpdate(u *Update) (*Update, error) {
	a.Lock()
	defer a.Unlock()
	uuid := u.Notification.UUID
	old, ok := a.updates[uuid]
	if ok {
		if old.Notification.Version > u.Notification.Version {
			return nil, errUpdateIsOlder
		} else if old.Notification.Version == u.Notification.Version {
			return nil, errUpdateIsAlreadyExist
		}
	}
	a.updates[uuid] = u
	return old, nil
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
