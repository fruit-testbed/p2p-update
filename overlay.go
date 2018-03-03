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

	peers map[string]*Peer

	automata *automata
	conn     *overlayConn
	stun     *stun.Client
	errCount int

	channelExpired time.Time
}

func NewOverlay(id string, rendezvousAddr, localAddr *net.UDPAddr, dataHandler DataHandler) (*Overlay, error) {
	overlay := &Overlay{
		ID:             id,
		DataHandler:    dataHandler,
		Reopen:         true,
		rendezvousAddr: rendezvousAddr,
		localAddr:      localAddr,
		peers:          make(map[string]*Peer),
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
	stateListening
	stateProcessingMessage
	stateMessageError
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
			transition{src: stateBinding, event: eventSuccess, dest: stateListening},
			transition{src: stateBinding, event: eventError, dest: stateBindError},
			transition{src: stateBindError, event: eventUnderLimit, dest: stateOpened},
			transition{src: stateBindError, event: eventOverLimit, dest: stateClosed},
			transition{src: stateListening, event: eventClose, dest: stateClosed},
			transition{src: stateListening, event: eventSuccess, dest: stateProcessingMessage},
			transition{src: stateListening, event: eventError, dest: stateMessageError},
			transition{src: stateListening, event: eventChannelExpired, dest: stateBinding},
			transition{src: stateProcessingMessage, event: eventSuccess, dest: stateListening},
			transition{src: stateProcessingMessage, event: eventError, dest: stateMessageError},
			transition{src: stateMessageError, event: eventUnderLimit, dest: stateListening},
			transition{src: stateMessageError, event: eventOverLimit, dest: stateBinding},
		},
		callbacks{
			stateOpening:           overlay.opening,
			stateOpened:            overlay.opened,
			stateBinding:           overlay.binding,
			stateBindError:         overlay.bindError,
			stateListening:         overlay.listening,
			stateProcessingMessage: overlay.processingMessage,
			stateMessageError:      overlay.messageError,
			stateClosed:            overlay.closed,
		},
	)
}

func (overlay *Overlay) Open() error {
	return overlay.automata.event(eventOpen)
}

func (overlay *Overlay) Close() error {
	return overlay.automata.event(eventClose)
}

func (overlay *Overlay) closed([]interface{}) {
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

func (overlay *Overlay) opening([]interface{}) {
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

func (overlay *Overlay) opened([]interface{}) {
	overlay.automata.event(eventBind)
}

func (overlay *Overlay) binding([]interface{}) {
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
			log.Println("failed getting mapped address:", err)
			overlay.automata.event(eventError)
		} else if err = overlay.processSessionTable(e.Message, new(stun.Message)); err != nil {
			log.Println("failed processing session table:", err)
			overlay.automata.event(eventError)
		} else {
			log.Println("XORMappedAddress", overlay.externalAddr)
			log.Println("LocalAddr", overlay.conn.conn.LocalAddr())
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

func (overlay *Overlay) bindError([]interface{}) {
	overlay.errCount++
	if overlay.errCount >= bindErrorsLimit {
		overlay.errCount = 0
		time.Sleep(backoffDuration)
		overlay.automata.event(eventOverLimit)
	} else {
		overlay.automata.event(eventUnderLimit)
	}
}

func (overlay *Overlay) listening([]interface{}) {
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
		switch peer := addr.(type) {
		case *net.UDPAddr:
			log.Printf("<- received a message from %s", peer.String())
			overlay.automata.event(eventSuccess, peer, buf[:n])
		default:
			log.Printf("unknown addr: %v", addr)
			overlay.automata.event(eventError)
		}

	}
}

func (overlay *Overlay) processingMessage(data []interface{}) {
	if len(data) < 2 {
		log.Fatalln("ERROR: processingMessage should receive two arguments from listening")
	}

	var (
		username stun.Username
		peer     Peer
		msg      []byte
		req, res stun.Message
		err      error
	)

	switch addr := data[0].(type) {
	case *net.UDPAddr:
		peer.Addr = *addr
	default:
		log.Fatalln("ERROR: first argument is not *Peer")
	}

	switch m := data[1].(type) {
	case []byte:
		msg = m
	default:
		log.Fatalln("ERROR: second argument is not []byte")
	}

	if !stun.IsMessage(msg) {
		log.Printf("the message is not a STUN message")
		overlay.automata.event(eventError)
	} else if _, err = req.Write(msg); err != nil {
		log.Println("failed to read message:", err)
		overlay.automata.event(eventError)
	} else if err = validateMessage(&req, nil); err != nil {
		log.Println("invalid STUN message:", err)
		overlay.automata.event(eventError)
	} else if err = username.GetFrom(&req); err != nil {
		log.Println("failed to get peerID:", err)
		overlay.automata.event(eventError)
	} else {
		peer.ID = username.String()
		if req.Type == stunBindingIndication {
			if err = overlay.processSessionTable(&req, &res); err != nil {
				log.Println("failed prcessing session table:", err)
				overlay.automata.event(eventError)
			} else {
				overlay.automata.event(eventSuccess)
			}
		} else if req.Type == stunDataRequest &&
			req.Contains(stun.AttrData) &&
			overlay.DataHandler != nil {
			if err = overlay.processDataRequest(&req, &res, &peer); err != nil {
				log.Printf("ERROR: failed processing data request: %v", err)
				overlay.automata.event(eventError)
			} else {
				overlay.automata.event(eventSuccess)
			}
		} else {
			log.Printf("ignored STUN message from %s", peer.String())
			overlay.automata.event(eventError)
		}
	}
}

func (overlay *Overlay) processSessionTable(req, res *stun.Message) error {
	var (
		st  *SessionTable
		err error
	)

	if st, err = GetSessionTableFrom(req); err != nil {
		return err
	}
	for _, peer := range *st {
		if err = overlay.bindChannelPeer(&peer); err != nil {
			log.Printf("WARNING: failed binding channel to %s - %v", peer.String(), err)
		} else {
			log.Printf("-> sent empty packet to opening channel to %s", peer.String())
		}
	}
	return nil
}

func (overlay *Overlay) bindChannelPeer(peer *Peer) error {
	if _, err := overlay.conn.conn.WriteToUDP([]byte{}, &peer.Addr); err != nil {
		return err
	}
	overlay.peers[peer.ID] = peer
	return nil
}

func (overlay *Overlay) processDataRequest(req, res *stun.Message, peer *Peer) error {
	var (
		data []byte
		err  error
	)

	if data, err = req.Get(stun.AttrData); err != nil {
		return fmt.Errorf("invalid data request from %s", peer.String())
	} else if err = overlay.DataHandler.HandleData(data, peer); err != nil {
		log.Println("DataHandler returned an error:", err)
		if err = overlay.buildDataErrorMessage(req, res, stun.CodeServerError); err != nil {
			return err
		}
	} else {
		log.Println("Successfully processing data")
		if err = overlay.buildDataSuccessMessage(req, res); err != nil {
			return err
		}
	}
	if _, err = overlay.conn.conn.WriteToUDP(res.Raw, &peer.Addr); err != nil {
		return errors.Wrapf(err, "failed send response to %s", peer.String())
	}
	log.Printf("-> sent response to %s", peer.String())
	return nil
}

func (overlay *Overlay) buildDataErrorMessage(req, res *stun.Message, ec stun.ErrorCode) error {
	return res.Build(
		stun.NewTransactionIDSetter(req.TransactionID),
		stunDataError,
		ec,
		stunSoftware,
		stun.NewUsername(overlay.ID),
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
}

func (overlay *Overlay) buildDataSuccessMessage(req, res *stun.Message) error {
	return res.Build(
		stun.NewTransactionIDSetter(req.TransactionID),
		stunDataSuccess,
		stunSoftware,
		stun.NewUsername(overlay.ID),
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
}

func (overlay *Overlay) messageError([]interface{}) {
	overlay.errCount++
	if overlay.errCount >= dataErrorsLimit {
		overlay.errCount = 0
		overlay.automata.event(eventOverLimit)
	} else {
		overlay.automata.event(eventUnderLimit)
	}
}

func (overlay *Overlay) HandleData(data []byte, peer *Peer) error {
	log.Printf("handle data from %s\n%s", peer.String(), string(data))
	return nil
}
