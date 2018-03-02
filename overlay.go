package main

import (
	"log"
	"net"
	"time"

	"github.com/gortc/stun"
	"github.com/pkg/errors"
)

type overlayConn struct {
	conn           *net.UDPConn
	rendezvousAddr *net.UDPAddr
	localAddr      *net.UDPAddr
	extAddr        stun.XORMappedAddress
}

func (oc *overlayConn) Open() error {
	var err error
	if oc.conn, err = net.ListenUDP("udp", oc.localAddr); err != nil {
		return errors.Wrap(err, "failed creating UDP connection")
	}
	return nil
}

func (oc *overlayConn) Read(p []byte) (n int, err error) {
	return oc.conn.Read(p)
}

func (oc *overlayConn) Write(p []byte) (n int, err error) {
	return oc.conn.WriteToUDP(p, oc.rendezvousAddr)
}

func (oc *overlayConn) Close() error {
	return oc.conn.Close()
}

type DataHandler interface {
	HandleData([]byte, *Peer) error
}

type Overlay struct {
	ID          string
	DataHandler DataHandler
	Reopen      bool

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
		ID: id,
		conn: &overlayConn{
			rendezvousAddr: rendezvousAddr,
			localAddr:      localAddr,
		},
		DataHandler: dataHandler,
		Reopen:      true,
		req:         new(stun.Message),
		res:         new(stun.Message),
	}
	overlay.createAutomata()
	return overlay, nil
}

const (
	bindErrorsLimit       = 5
	bindingDeadline       = 30 * time.Second
	dataErrorsLimit       = 5
	receivingDataDeadline = 30 * time.Second
	backoffDuration       = 10 * time.Second
	bufferSize            = 64 * 1024 // buffer size to read UDP packet
	channelDuration       = 45 * time.Second
)

const (
	stateClosed = iota
	stateOpened
	stateBinding
	stateBindError
	stateReceivingData
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
)

func (overlay *Overlay) createAutomata() {
	overlay.automata = NewAutomata(
		stateClosed,
		[]transition{
			transition{src: stateClosed, event: eventOpen, dest: stateOpened},
			transition{src: stateOpened, event: eventClose, dest: stateClosed},
			transition{src: stateOpened, event: eventBind, dest: stateBinding},
			transition{src: stateBinding, event: eventSuccess, dest: stateReceivingData},
			transition{src: stateBinding, event: eventError, dest: stateBindError},
			transition{src: stateBindError, event: eventUnderLimit, dest: stateOpened},
			transition{src: stateBindError, event: eventOverLimit, dest: stateClosed},
			transition{src: stateReceivingData, event: eventClose, dest: stateClosed},
			transition{src: stateReceivingData, event: eventSuccess, dest: stateProcessingData},
			transition{src: stateReceivingData, event: eventError, dest: stateDataError},
			transition{src: stateProcessingData, event: eventSuccess, dest: stateReceivingData},
			transition{src: stateProcessingData, event: eventError, dest: stateDataError},
			transition{src: stateDataError, event: eventUnderLimit, dest: stateReceivingData},
			transition{src: stateDataError, event: eventOverLimit, dest: stateBinding},
		},
		callbacks{
			stateOpened:         overlay.opened,
			stateBinding:        overlay.binding,
			stateBindError:      overlay.bindError,
			stateReceivingData:  overlay.receivingData,
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
	go func() {
		if err := overlay.stun.Close(); err != nil {
			log.Printf("failed to close connection: %v", err)
		} else {
			log.Println("stun client closed")
		}
	}()
	overlay.errCount = 0
	log.Println("closed")

	if overlay.Reopen {
		log.Println("reopen")
		overlay.automata.event(eventOpen)
	} else {
		log.Println("overlay is stopped")
	}
}

func (overlay *Overlay) opened() {
	var err error

	if err = overlay.conn.Open(); err != nil {
		log.Printf("failed opening UDP connection: %v", err)
	}
	overlay.stun, err = stun.NewClient(
		stun.ClientOptions{
			Connection: overlay.conn,
		})
	if err != nil {
		log.Printf("Failed dialing the STUN server at %s - %v", overlay.conn.rendezvousAddr, err)
	}
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
		} else if err = overlay.conn.extAddr.GetFrom(e.Message); err != nil {
			log.Println("Failed getting mapped address:", err)
			overlay.automata.event(eventError)
		} else {
			log.Println("XORMappedAddress", overlay.conn.extAddr)
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

func (overlay *Overlay) receivingData() {
	var (
		deadline = time.Now().Add(receivingDataDeadline)
		buf      = make([]byte, bufferSize)

		n    int
		addr net.Addr
		err  error
	)

	if deadline.After(overlay.channelExpired) {
		deadline = overlay.channelExpired
	}
	log.Println("channel will expire within", overlay.channelExpired.Sub(time.Now()))

	if err = overlay.conn.conn.SetDeadline(deadline); err != nil {
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
