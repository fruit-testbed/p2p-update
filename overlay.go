package main

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/gortc/stun"
	"github.com/pkg/errors"
)

type overlayConn struct {
	conn           *net.UDPConn
	rendezvousAddr *net.UDPAddr
}

func newOverlayConn(rendezvousAddr string) (*overlayConn, error) {
	var (
		addr *net.UDPAddr
		err  error
	)
	if addr, err = net.ResolveUDPAddr("udp", rendezvousAddr); err != nil {
		return nil, errors.Wrapf(err, "failed resolving rendezvous address %s", rendezvousAddr)
	}
	return &overlayConn{
		rendezvousAddr: addr,
	}, nil
}

func (oc *overlayConn) Open() error {
	var err error
	if oc.conn, err = net.ListenUDP("udp", nil); err != nil {
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
	sync.Mutex
	ID          string
	automata    *automata
	conn        *overlayConn
	stun        *stun.Client
	errCount    int
	DataHandler DataHandler

	addr *net.UDPAddr
	msg  []byte
	res  *stun.Message
	req  *stun.Message
}

func NewOverlay(id string, rendezvousAddr string, dataHandler DataHandler) (*Overlay, error) {
	var (
		conn *overlayConn
		err  error
	)
	if conn, err = newOverlayConn(rendezvousAddr); err != nil {
		return nil, err
	}
	overlay := &Overlay{
		ID:          id,
		conn:        conn,
		DataHandler: dataHandler,
		req:         new(stun.Message),
		res:         new(stun.Message),
	}
	overlay.createAutomata()
	return overlay, nil
}

const (
	bindErrorsLimit       = 5
	bindingDeadline       = 10 * time.Second
	dataErrorsLimit       = 10
	receivingDataDeadline = 30 * time.Second
	backoffDuration       = 10 * time.Second
	bufferSize            = 64 * 1024 // buffer size to read UDP packet
)

const (
	stateClosed = iota
	stateStopped
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
	eventErrorsUnderLimit
	eventErrorsOverLimit
)

func (overlay *Overlay) createAutomata() {
	overlay.automata = NewAutomata(
		stateClosed,
		[]transition{
			transition{src: stateClosed, event: eventOpen, dest: stateStopped},
			transition{src: stateStopped, event: eventClose, dest: stateClosed},
			transition{src: stateStopped, event: eventBind, dest: stateBinding},
			transition{src: stateBinding, event: eventSuccess, dest: stateReceivingData},
			transition{src: stateBinding, event: eventError, dest: stateBindError},
			transition{src: stateBindError, event: eventErrorsUnderLimit, dest: stateStopped},
			transition{src: stateBindError, event: eventErrorsOverLimit, dest: stateClosed},
			transition{src: stateReceivingData, event: eventClose, dest: stateClosed},
			transition{src: stateReceivingData, event: eventSuccess, dest: stateProcessingData},
			transition{src: stateReceivingData, event: eventError, dest: stateDataError},
			transition{src: stateProcessingData, event: eventSuccess, dest: stateReceivingData},
			transition{src: stateProcessingData, event: eventError, dest: stateDataError},
			transition{src: stateDataError, event: eventErrorsUnderLimit, dest: stateReceivingData},
			transition{src: stateDataError, event: eventErrorsOverLimit, dest: stateStopped},
		},
		callbacks{
			stateStopped:        overlay.stopped,
			stateBinding:        overlay.binding,
			stateBindError:      overlay.bindError,
			stateReceivingData:  overlay.receivingData,
			stateProcessingData: overlay.processingData,
			stateDataError:      overlay.dataError,
			stateClosed:         func() {}, // do nothing
		},
	)
}

func (overlay *Overlay) Open() error {
	overlay.Lock()
	if overlay.automata.current != stateClosed {
		overlay.Unlock()
		return fmt.Errorf("current state (%d) is not closed", overlay.automata.current)
	}

	var err error
	if err = overlay.conn.Open(); err != nil {
		overlay.Unlock()
		return errors.Wrap(err, "failed opening UDP connection")
	}
	overlay.stun, err = stun.NewClient(
		stun.ClientOptions{
			Connection: overlay.conn,
		})
	if err != nil {
		overlay.Unlock()
		return errors.Wrapf(err, "Failed dialing the STUN server at %s", overlay.conn.rendezvousAddr)
	}
	overlay.Unlock()
	return overlay.automata.event(eventOpen)
}

func (overlay *Overlay) Close() error {
	overlay.Lock()
	switch overlay.automata.current {
	case stateStopped, stateReceivingData:
		overlay.Unlock()
		return fmt.Errorf("current state (%d) is not stopped or receivingData", overlay.automata.current)
	}
	if err := overlay.stun.Close(); err != nil {
		overlay.Unlock()
		return errors.Wrap(err, "failed to close connection")
	}
	overlay.errCount = 0
	overlay.Unlock()
	return overlay.automata.event(eventClose)
}

func (overlay *Overlay) stopped() {
	overlay.automata.event(eventBind)
}

func (overlay *Overlay) binding() {
	deadline := time.Now().Add(bindingDeadline)
	handler := stun.HandlerFunc(func(e stun.Event) {
		var xorAddr stun.XORMappedAddress
		if e.Error != nil {
			log.Println("bindingError", e.Error)
		} else if e.Message == nil {
			log.Println("bindingError", errors.New("bindReq received an empty message"))
		} else if err := validateMessage(e.Message, &stun.BindingSuccess); err != nil {
			log.Println("bindingError", errors.Wrap(err, "bindReq received an invalid message:"))
		} else if err = xorAddr.GetFrom(e.Message); err != nil {
			log.Println("Failed getting mapped address:", err)
		} else {
			log.Println("AttrMappedAddress", e.Message.Contains(stun.AttrMappedAddress))
			log.Println("XORMappedAddress", xorAddr)
			log.Println("LocalAddr", overlay.conn.conn.LocalAddr())
			log.Println("RemoteAddr", overlay.conn.conn.RemoteAddr())
			log.Println("bindingSuccess")
			overlay.automata.event(eventSuccess)
		}
		overlay.automata.event(eventError)
	})
	if err := overlay.stun.Start(overlay.bindingRequestMessage(), deadline, handler); err != nil {
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
	overlay.Lock()
	overlay.errCount++
	if overlay.errCount >= bindErrorsLimit {
		overlay.errCount = 0
		overlay.Unlock()
		// replace below two lines with: `overlay.automata.event(eventErrorsOverLimit)`
		// to disable infinite loop
		time.Sleep(backoffDuration)
		overlay.automata.event(eventErrorsUnderLimit)
	} else {
		overlay.Unlock()
		overlay.automata.event(eventErrorsUnderLimit)
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

	if err = overlay.conn.conn.SetReadDeadline(deadline); err != nil {
		log.Printf("failed to set read deadline: %v", err)
		overlay.automata.event(eventError)
	} else if n, addr, err = overlay.conn.conn.ReadFrom(buf); err != nil {
		log.Printf("failed to read the message: %v", err)
		overlay.automata.event(eventError)
	} else if !stun.IsMessage(buf[:n]) {
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
	overlay.Lock()
	overlay.errCount++
	if overlay.errCount >= dataErrorsLimit {
		overlay.errCount = 0
		overlay.Unlock()
		overlay.automata.event(eventErrorsOverLimit)
	} else {
		overlay.Unlock()
		overlay.automata.event(eventErrorsUnderLimit)
	}
}

func (overlay *Overlay) HandleData(data []byte, peer *Peer) error {
	log.Printf("receive data from %s", peer.String())
	fmt.Println(string(data))
	return nil
}
