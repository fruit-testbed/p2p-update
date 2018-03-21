package main

import (
	"bytes"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/anacrolix/torrent"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/zeebo/bencode"
)

// Update represents a system update that should be downloaded and deployed on
// the system. It also has to be distributed to other peers.
type Update struct {
	sync.RWMutex

	Metainfo Metainfo `json:"metainfo"`
	Filename string   `json:"filename"`

	torrent  *torrent.Torrent
	stopped  bool
	automata *Automata
}

// NewUpdate creates an Update instance from given torrent-file++.
func NewUpdate(b []byte) (*Update, error) {
	u := Update{}
	err := bencode.DecodeBytes(b, &u.Metainfo)

	// TODO: complete below Automata
	u.automata = NewAutomata(
		stateDeleted,
		[]Transition{
			Transition{Src: stateDeleted, Event: eventCreate, Dest: stateCreated},
			Transition{Src: stateCreated, Event: eventDownload, Dest: stateDownloading},
			Transition{Src: stateCreated, Event: eventDelete, Dest: stateDeleted},
			Transition{Src: stateDownloading, Event: eventStop, Dest: stateCreated},
			Transition{Src: stateDownloading, Event: eventError, Dest: stateDownloadError},
			Transition{Src: stateDownloading, Event: eventSuccess, Dest: stateDownloaded},
			Transition{Src: stateDownloadError, Event: eventUnderLimit, Dest: stateDownloading},
			Transition{Src: stateDownloadError, Event: eventOverLimit, Dest: stateCreated},
			Transition{Src: stateDownloaded, Event: eventDeploy, Dest: stateDeploying},
			Transition{Src: stateDownloaded, Event: eventDelete, Dest: stateDeleted},
			Transition{Src: stateDeploying, Event: eventStop, Dest: stateDownloaded},
			Transition{Src: stateDeploying, Event: eventSuccess, Dest: stateDeployed},
			Transition{Src: stateDeploying, Event: eventError, Dest: stateDeployError},
			Transition{Src: stateDeployError, Event: eventUnderLimit, Dest: stateDeploying},
			Transition{Src: stateDeployError, Event: eventOverLimit, Dest: stateDownloaded},
			Transition{Src: stateDeployed, Event: eventDelete, Dest: stateDeleted},
		},
		callbacks{},
	)

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
