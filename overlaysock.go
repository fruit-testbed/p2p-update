package main

import (
	"fmt"
	"log"
	"net"
	"time"

	"github.com/gortc/stun"
	"github.com/pkg/errors"
)

type overlayUDPConn struct {
	conn           *net.UDPConn
	rendezvousAddr *net.UDPAddr
}

func newOverlayUDPConn(rendezvousAddr, localAddr *net.UDPAddr) (*overlayUDPConn, error) {
	var (
		conn *net.UDPConn
		err  error
	)

	if conn, err = net.ListenUDP("udp", localAddr); err != nil {
		return nil, errors.Wrap(err, "failed creating UDP connection")
	}
	log.Println("connection is opened at", conn.LocalAddr().String())
	return &overlayUDPConn{
		conn:           conn,
		rendezvousAddr: rendezvousAddr,
	}, nil
}

func (oc *overlayUDPConn) Read(p []byte) (n int, err error) {
	if oc.conn == nil {
		return -1, fmt.Errorf("connection is not opened")
	}
	return oc.conn.Read(p)
}

func (oc *overlayUDPConn) Write(p []byte) (n int, err error) {
	if oc.conn == nil {
		return -1, fmt.Errorf("connection is not opened")
	}
	return oc.conn.WriteToUDP(p, oc.rendezvousAddr)
}

func (oc *overlayUDPConn) Close() error {
	if oc.conn == nil {
		return nil
	}
	return oc.conn.Close()
}

// OverlayConn is an implementation of net.Conn interface for a overlay network
// that uses STUN punching hole technique to enable peer-to-peer communications
// for nodes behind NATs.
type OverlayConn struct {
	ID     PeerID
	Reopen bool

	rendezvousAddr *net.UDPAddr
	localAddr      *net.UDPAddr
	externalAddr   stun.XORMappedAddress

	automata *automata
	conn     *overlayUDPConn
	stun     *stun.Client
	errCount int

	channelExpired time.Time

	readDeadline  *time.Time
	writeDeadline *time.Time
}

// NewOverlayConn creates an overlay peer-to-peer connection that implements STUN
// punching hole technique to directly communicate to peers behind NATs.
func NewOverlayConn(rendezvousAddr, localAddr *net.UDPAddr) (*OverlayConn, error) {
	var (
		pid *PeerID
		err error
	)

	if pid, err = localID(); err != nil {
		return nil, errors.Wrap(err, "failed to get local ID")
	}
	log.Printf("local peer ID: %s", pid.String())
	overlay := &OverlayConn{
		ID:             *pid,
		Reopen:         true,
		rendezvousAddr: rendezvousAddr,
		localAddr:      localAddr,
	}
	overlay.createAutomata()
	overlay.automata.event(eventOpen)
	return overlay, nil
}

const (
	bindWaitTime      = 30 * time.Second
	bindErrorsLimit   = 10
	listenWaitTime    = 30 * time.Second
	listenErrorsLimit = 10
	readBufferSize    = 64 * 1024 // buffer size to read UDP packet
	backoffDuration   = 10 * time.Second
	channelDuration   = 60 * time.Second
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

func (overlay *OverlayConn) createAutomata() {
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

func (overlay *OverlayConn) closed([]interface{}) {
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

func (overlay *OverlayConn) opening([]interface{}) {
	var err error

	if overlay.conn, err = newOverlayUDPConn(overlay.rendezvousAddr, overlay.localAddr); err != nil {
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
			log.Printf("local address: %s", overlay.conn.conn.LocalAddr().String())
			overlay.automata.event(eventSuccess)
		}
	}
}

func (overlay *OverlayConn) opened([]interface{}) {
	overlay.automata.event(eventBind)
}

func (overlay *OverlayConn) binding([]interface{}) {
	var (
		msg *stun.Message
		err error
	)

	deadline := time.Now().Add(bindWaitTime)

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
	} else if msg, err = overlay.bindingRequestMessage(); err != nil {
		log.Println("failed building bindingRequestMessage", err)
		overlay.automata.event(eventError)
	} else if err = overlay.stun.Start(msg, deadline, handler); err != nil {
		log.Println("binding failed:", err)
		overlay.automata.event(eventError)
	}
}

func (overlay *OverlayConn) bindingRequestMessage() (*stun.Message, error) {
	var (
		laddr   = overlay.conn.conn.LocalAddr()
		addr    *net.UDPAddr
		xorAddr stun.XORMappedAddress
		err     error
	)

	if addr, err = net.ResolveUDPAddr(laddr.Network(), laddr.String()); err != nil {
		return nil, err
	}
	xorAddr.IP = addr.IP
	xorAddr.Port = addr.Port

	return stun.Build(
		stun.TransactionID,
		stun.BindingRequest,
		xorAddr,
		stunSoftware,
		&overlay.ID,
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
}

func (overlay *OverlayConn) bindError([]interface{}) {
	overlay.errCount++
	if overlay.errCount >= bindErrorsLimit {
		overlay.errCount = 0
		time.Sleep(backoffDuration)
		overlay.automata.event(eventOverLimit)
	} else {
		overlay.automata.event(eventUnderLimit)
	}
}

func (overlay *OverlayConn) listening([]interface{}) {
	var (
		deadline = time.Now().Add(listenWaitTime)
		buf      = make([]byte, readBufferSize)

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

func (overlay *OverlayConn) processingMessage(data []interface{}) {
	if len(data) < 2 {
		log.Fatalln("ERROR: processingMessage should receive two arguments from listening")
	}

	var (
		pid      PeerID
		peer     Peer
		msg      []byte
		req, res stun.Message
		err      error
	)

	switch addr := data[0].(type) {
	case *net.UDPAddr:
		peer.ExternalAddr = *addr
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
		log.Printf("!!! %s sent a message that is not a STUN message", peer.ExternalAddr.String())
		overlay.automata.event(eventError)
	} else if _, err = req.Write(msg); err != nil {
		log.Printf("failed to read message from %s: %v", peer.ExternalAddr.String(), err)
		overlay.automata.event(eventError)
	} else if err = validateMessage(&req, nil); err != nil {
		log.Printf("%s sent invalid STUN message: %v", peer.ExternalAddr.String(), err)
		overlay.automata.event(eventError)
	} else if err = pid.GetFrom(&req); err != nil {
		log.Printf("failed to get peerID of %s: %v", peer.ExternalAddr.String(), err)
		overlay.automata.event(eventError)
	} else {
		peer.ID = pid
		if req.Type == stunBindingIndication {
			if err = overlay.processSessionTable(&req, &res); err != nil {
				log.Println("failed prcessing session table:", err)
				overlay.automata.event(eventError)
			} else {
				overlay.automata.event(eventSuccess)
			}
		} else if req.Type == stun.BindingRequest {
			// TODO: receive ping, reply with internal and external addresses
			log.Printf("!!! failed prcessing binding request from %s", peer.String())
			overlay.automata.event(eventError)
		} else if req.Type == stunDataRequest && req.Contains(stun.AttrData) {
			if err = overlay.processDataRequest(&req, &res, &peer); err != nil {
				log.Printf("ERROR: failed processing data request of %s: %v", peer.String(), err)
				overlay.automata.event(eventError)
			} else {
				overlay.automata.event(eventSuccess)
			}
		} else {
			log.Printf("!!! ignored STUN message from %s", peer.String())
			overlay.automata.event(eventError)
		}
	}
}

func (overlay *OverlayConn) processSessionTable(req, res *stun.Message) error {
	var (
		st   *SessionTable
		addr *net.UDPAddr
		msg  *stun.Message
		err  error
	)

	if st, err = GetSessionTableFrom(req); err != nil {
		return err
	}
	if msg, err = overlay.bindingRequestMessage(); err != nil {
		return errors.Wrap(err, "failed creating binding request message")
	}
	for id, addrs := range *st {
		if id == overlay.ID {
			continue
		}
		if addr = addrs[0]; addr.IP.Equal(overlay.externalAddr.IP) {
			addr = addrs[1]
		}
		if err = overlay.bindingPeer(addr, msg); err != nil {
			log.Printf("WARNING: failed binding channel to %s[%s][%s] - %v",
				id, addrs[0].String(), addrs[1].String(), err)
		} else {
			log.Printf("-> sent empty packet to opening channel to %s[%s][%s] ",
				id, addrs[0].String(), addrs[1].String())
		}
	}
	return nil
}

func (overlay *OverlayConn) bindingPeer(addr *net.UDPAddr, msg *stun.Message) error {
	// TODO: Send BindingRequest message. When the peer receives, then it
	//       replies with success message and add the sender to peer's
	// session table with expiration of 60 seconds.
	if _, err := overlay.conn.conn.WriteToUDP(msg.Raw, addr); err != nil {
		return err
	}
	return nil
}

func (overlay *OverlayConn) processDataRequest(req, res *stun.Message, peer *Peer) error {
	var (
		data []byte
		err  error
	)

	if data, err = req.Get(stun.AttrData); err != nil {
		return fmt.Errorf("invalid data request from %s", peer.String())
	}
	// TODO: process the data
	log.Printf("%s sent:\n%s", peer.String(), string(data))
	log.Println("Successfully processing data")
	if err = overlay.buildDataSuccessMessage(req, res); err != nil {
		return err
	}
	if _, err = overlay.conn.conn.WriteToUDP(res.Raw, &peer.ExternalAddr); err != nil {
		return errors.Wrapf(err, "failed send response to %s", peer.String())
	}
	log.Printf("-> sent response to %s", peer.String())
	return nil
}

func (overlay *OverlayConn) buildDataErrorMessage(req, res *stun.Message, ec stun.ErrorCode) error {
	return res.Build(
		stun.NewTransactionIDSetter(req.TransactionID),
		stunDataError,
		ec,
		stunSoftware,
		&overlay.ID,
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
}

func (overlay *OverlayConn) buildDataSuccessMessage(req, res *stun.Message) error {
	return res.Build(
		stun.NewTransactionIDSetter(req.TransactionID),
		stunDataSuccess,
		stunSoftware,
		&overlay.ID,
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
}

func (overlay *OverlayConn) messageError([]interface{}) {
	overlay.errCount++
	if overlay.errCount >= listenErrorsLimit {
		overlay.errCount = 0
		overlay.automata.event(eventOverLimit)
	} else {
		overlay.automata.event(eventUnderLimit)
	}
}

// Read reads a gossip message sent by other
func (overlay *OverlayConn) Read(b []byte) (int, error) {
	// TODO: implement
	return 0, nil
}

// Write sends a gossip message to other nodes
func (overlay *OverlayConn) Write(b []byte) (int, error) {
	// TODO: handle multi-packets payload
	return 0, nil
}

// Close closes the overlay.
func (overlay *OverlayConn) Close() error {
	return overlay.automata.event(eventClose)
}

// LocalAddr returns local (internal) address of this overlay.
func (overlay *OverlayConn) LocalAddr() net.Addr {
	return overlay.localAddr
}

// RemoteAddr returns remote (external) address of this overlay
func (overlay *OverlayConn) RemoteAddr() net.Addr {
	if addr, err := net.ResolveUDPAddr("udp", overlay.externalAddr.String()); err == nil {
		return addr
	}
	return nil
}

// SetDeadline sets read and write dealines
func (overlay *OverlayConn) SetDeadline(t time.Time) error {
	overlay.readDeadline, overlay.writeDeadline = &t, &t
	return nil
}

// SetReadDeadline sets read deadline
func (overlay *OverlayConn) SetReadDeadline(t time.Time) error {
	overlay.readDeadline = &t
	return nil
}

// SetWriteDeadline sets write deadline
func (overlay *OverlayConn) SetWriteDeadline(t time.Time) error {
	overlay.writeDeadline = &t
	return nil
}
