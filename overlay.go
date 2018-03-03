package main

import (
	"fmt"
	"log"
	"net"
	"time"

	"github.com/gortc/stun"
	"github.com/pkg/errors"
)

type overlayConn struct {
	conn           *net.UDPConn
	rendezvousAddr *net.UDPAddr
}

func NewOverlayConn(rendezvousAddr, localAddr *net.UDPAddr) (*overlayConn, error) {
	var (
		conn *net.UDPConn
		err  error
	)

	if conn, err = net.ListenUDP("udp", localAddr); err != nil {
		return nil, errors.Wrap(err, "failed creating UDP connection")
	}
	log.Println("connection is opened at", conn.LocalAddr().String())
	return &overlayConn{
		conn:           conn,
		rendezvousAddr: rendezvousAddr,
	}, nil
}

func (oc *overlayConn) Read(p []byte) (n int, err error) {
	if oc.conn == nil {
		return -1, fmt.Errorf("connection is not opened")
	}
	return oc.conn.Read(p)
}

func (oc *overlayConn) Write(p []byte) (n int, err error) {
	if oc.conn == nil {
		return -1, fmt.Errorf("connection is not opened")
	}
	return oc.conn.WriteToUDP(p, oc.rendezvousAddr)
}

func (oc *overlayConn) Close() error {
	if oc.conn == nil {
		return nil
	}
	return oc.conn.Close()
}

type DataHandler interface {
	HandleData([]byte, *Peer) error
}

type Overlay struct {
	ID          string
	DataHandler DataHandler
	Reopen      bool

	rendezvousAddr *net.UDPAddr
	localAddr      *net.UDPAddr
	externalAddr   stun.XORMappedAddress

	automata *automata
	conn     *overlayConn
	stun     *stun.Client
	errCount int

	channelExpired time.Time

	addr *net.UDPAddr
	msg  []byte
	res  *stun.Message
	req  *stun.Message
}

func NewOverlay(id string, rendezvousAddr, localAddr *net.UDPAddr, dataHandler DataHandler) (*Overlay, error) {
	overlay := &Overlay{
		ID:             id,
		DataHandler:    dataHandler,
		Reopen:         true,
		rendezvousAddr: rendezvousAddr,
		localAddr:      localAddr,
		req:            new(stun.Message),
		res:            new(stun.Message),
	}
	overlay.createAutomata()
	return overlay, nil
}

const (
	bindErrorsLimit     = 3
	bindingDeadline     = 3 * time.Second
	dataErrorsLimit     = 3
	readingDataDeadline = 3 * time.Second
	backoffDuration     = 3 * time.Second
	bufferSize          = 64 * 1024 // buffer size to read UDP packet
	channelDuration     = 10 * time.Second
)

const (
	stateClosed = iota
	stateOpening
	stateOpened
	stateBinding
	stateBindError
	stateReadingData
	stateProcessingData
	stateDataError
)

const (
	eventOpen = iota + 100
	eventClose
	eventBind
	eventSuccess
	eventError
	eventUnderLimit
	eventOverLimit
	eventChannelExpired
)

func (overlay *Overlay) createAutomata() {
	overlay.automata = NewAutomata(
		stateClosed,
		[]transition{
			transition{src: stateClosed, event: eventOpen, dest: stateOpening},
			transition{src: stateOpening, event: eventSuccess, dest: stateOpened},
			transition{src: stateOpening, event: eventError, dest: stateClosed},
			transition{src: stateOpened, event: eventClose, dest: stateClosed},
			transition{src: stateOpened, event: eventBind, dest: stateBinding},
			transition{src: stateBinding, event: eventSuccess, dest: stateReadingData},
			transition{src: stateBinding, event: eventError, dest: stateBindError},
			transition{src: stateBindError, event: eventUnderLimit, dest: stateOpened},
			transition{src: stateBindError, event: eventOverLimit, dest: stateClosed},
			transition{src: stateReadingData, event: eventClose, dest: stateClosed},
			transition{src: stateReadingData, event: eventSuccess, dest: stateProcessingData},
			transition{src: stateReadingData, event: eventError, dest: stateDataError},
			transition{src: stateReadingData, event: eventChannelExpired, dest: stateBinding},
			transition{src: stateProcessingData, event: eventSuccess, dest: stateReadingData},
			transition{src: stateProcessingData, event: eventError, dest: stateDataError},
			transition{src: stateDataError, event: eventUnderLimit, dest: stateReadingData},
			transition{src: stateDataError, event: eventOverLimit, dest: stateBinding},
		},
		callbacks{
			stateOpening:        overlay.opening,
			stateOpened:         overlay.opened,
			stateBinding:        overlay.binding,
			stateBindError:      overlay.bindError,
			stateReadingData:    overlay.readingData,
			stateProcessingData: overlay.processingData,
			stateDataError:      overlay.dataError,
			stateClosed:         overlay.closed,
		},
	)
}

func (overlay *Overlay) Open() error {
	return overlay.automata.event(eventOpen)
}

func (overlay *Overlay) Close() error {
	return overlay.automata.event(eventClose)
}

func (overlay *Overlay) closed() {
	log.Println("closing")

	conn, stun := overlay.conn, overlay.stun
	go func() {
		if conn != nil {
			conn.Close()
		}
		if stun != nil {
			stun.Close()
		}
		log.Println("old conn and stun are closed")
	}()

	overlay.conn = nil
	overlay.stun = nil
	overlay.errCount = 0
	log.Println("closed")

	if overlay.Reopen {
		log.Println("reopen")
		overlay.automata.event(eventOpen)
	} else {
		log.Println("overlay is stopped")
	}
}

func (overlay *Overlay) opening() {
	var err error

	if overlay.conn, err = NewOverlayConn(overlay.rendezvousAddr, overlay.localAddr); err != nil {
		log.Printf("failed opening UDP connection (backing off for %v): %v",
			backoffDuration, err)
		time.Sleep(backoffDuration)
		overlay.automata.event(eventError)
	} else {
		overlay.stun, err = stun.NewClient(
			stun.ClientOptions{
				Connection: overlay.conn,
			})
		if err != nil {
			log.Printf("failed dialing the STUN server at %s (backing off for %v) - %v",
				backoffDuration, overlay.rendezvousAddr, err)
			overlay.automata.event(eventError)
		} else {
			overlay.automata.event(eventSuccess)
		}
	}
}

func (overlay *Overlay) opened() {
	overlay.automata.event(eventBind)
}

func (overlay *Overlay) binding() {
	var err error

	deadline := time.Now().Add(bindingDeadline)

	handler := stun.HandlerFunc(func(e stun.Event) {
		if e.Error != nil {
			log.Println("bindingError", e.Error)
			overlay.automata.event(eventError)
		} else if e.Message == nil {
			log.Println("bindingError", errors.New("bindReq received an empty message"))
			overlay.automata.event(eventError)
		} else if err := validateMessage(e.Message, &stun.BindingSuccess); err != nil {
			log.Println("bindingError", errors.Wrap(err, "bindReq received an invalid message:"))
			overlay.automata.event(eventError)
		} else if err = overlay.externalAddr.GetFrom(e.Message); err != nil {
			log.Println("Failed getting mapped address:", err)
			overlay.automata.event(eventError)
		} else {
			log.Println("XORMappedAddress", overlay.externalAddr)
			log.Println("LocalAddr", overlay.conn.conn.LocalAddr())
			log.Println("RemoteAddr", overlay.conn.conn.RemoteAddr())
			log.Println("bindingSuccess")
			overlay.channelExpired = time.Now().Add(channelDuration)
			overlay.automata.event(eventSuccess)
		}
	})

	if err = overlay.conn.conn.SetDeadline(deadline); err != nil {
		log.Println("failed setting connection read/write deadline")
		overlay.automata.event(eventError)
	} else if err = overlay.stun.Start(overlay.bindingRequestMessage(), deadline, handler); err != nil {
		log.Println("binding failed:", err)
		overlay.automata.event(eventError)
	}
}

func (overlay *Overlay) bindingRequestMessage() *stun.Message {
	return stun.MustBuild(
		stun.TransactionID,
		stun.BindingRequest,
		stunSoftware,
		stun.NewUsername(overlay.ID),
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
}

func (overlay *Overlay) bindError() {
	overlay.errCount++
	if overlay.errCount >= bindErrorsLimit {
		overlay.errCount = 0
		time.Sleep(backoffDuration)
		overlay.automata.event(eventOverLimit)
	} else {
		overlay.automata.event(eventUnderLimit)
	}
}

func (overlay *Overlay) readingData() {
	var (
		deadline = time.Now().Add(readingDataDeadline)
		buf      = make([]byte, bufferSize)

		n    int
		addr net.Addr
		err  error
	)

	if deadline.After(overlay.channelExpired) {
		deadline = overlay.channelExpired
	}
	log.Println("channel will expire within", overlay.channelExpired.Sub(time.Now()))

	if deadline.Before(time.Now()) {
		log.Println("channel has expired")
		overlay.automata.event(eventChannelExpired)
	} else if err = overlay.conn.conn.SetDeadline(deadline); err != nil {
		log.Printf("failed to set read deadline: %v", err)
		overlay.automata.event(eventError)
	} else if n, addr, err = overlay.conn.conn.ReadFrom(buf); err != nil {
		log.Printf("failed to read the message: %v", err)
		overlay.automata.event(eventError)
	} else {
		overlay.channelExpired = time.Now().Add(channelDuration)
		if !stun.IsMessage(buf[:n]) {
			log.Printf("received not a STUN message")
			overlay.automata.event(eventError)
		} else {
			switch peer := addr.(type) {
			case *net.UDPAddr:
				overlay.addr = peer
				log.Printf("received STUN message from %v:%d", peer.IP, peer.Port)
				overlay.msg = buf[:n]
				overlay.automata.event(eventSuccess)
			default:
				log.Printf("unknown addr: %v", overlay.addr)
				overlay.automata.event(eventError)
			}
		}
	}
}

func (overlay *Overlay) processingData() {
	var (
		username stun.Username
		data     []byte
		err      error
	)

	defer func() {
		overlay.addr, overlay.msg = nil, nil
		overlay.req.Reset()
		overlay.res.Reset()
	}()

	if _, err = overlay.req.Write(overlay.msg); err != nil {
		err = errors.Wrap(err, "failed to read message")
	} else if err = validateMessage(overlay.req, &stunDataRequest); err != nil {
		err = errors.Wrap(err, "invalid STUN data message")
	} else if err := username.GetFrom(overlay.req); err != nil {
		err = errors.Wrap(err, "failed to get peerID")
	} else if overlay.req.Contains(stun.AttrData) && overlay.DataHandler != nil {
		peer := Peer{
			ID:   username.String(),
			IP:   overlay.addr.IP,
			Port: overlay.addr.Port,
		}
		if data, err = overlay.req.Get(stun.AttrData); err != nil {
			err = errors.Wrap(err, "failed get the data from STUN message")
		} else if err = overlay.DataHandler.HandleData(data, &peer); err != nil {
			err = errors.Wrap(err, "DataHandler returned an error")
		}
	}

	if respErr := overlay.buildDataResponseMessage(err == nil); respErr != nil {
		log.Printf("failed building data response message: %v", respErr)
		overlay.automata.event(eventError)
	} else {
		if _, writeErr := overlay.conn.conn.WriteToUDP(overlay.res.Raw, overlay.addr); writeErr != nil {
			log.Printf("failed WriteTo %v - %v", overlay.addr, writeErr)
			overlay.automata.event(eventError)
		} else if err != nil {
			overlay.automata.event(eventError)
		} else {
			overlay.automata.event(eventSuccess)
		}
	}
}

func (overlay *Overlay) buildDataResponseMessage(success bool) error {
	var messageType stun.MessageType

	if messageType = stunDataSuccess; !success {
		messageType = stunDataError
	}
	return overlay.res.Build(
		stun.NewTransactionIDSetter(overlay.req.TransactionID),
		messageType,
		stunSoftware,
		stun.NewUsername(overlay.ID),
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
}

func (overlay *Overlay) dataError() {
	overlay.errCount++
	if overlay.errCount >= dataErrorsLimit {
		overlay.errCount = 0
		overlay.automata.event(eventOverLimit)
	} else {
		overlay.automata.event(eventUnderLimit)
	}
}

func (overlay *Overlay) HandleData(data []byte, peer *Peer) error {
	log.Printf("receive data from %s\n%s", peer.String(), string(data))
	return nil
}
