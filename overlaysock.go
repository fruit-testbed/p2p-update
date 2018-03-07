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

	automata *Automata
	conn     *overlayUDPConn
	stun     *stun.Client
	errCount int

	channelExpired time.Time
	msg            []byte
	addr           *net.UDPAddr
	peers          SessionTable
	peerDataChan   chan []byte

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

	if pid, err = LocalPeerID(); err != nil {
		return nil, errors.Wrap(err, "failed to get local ID")
	}
	log.Printf("local peer ID: %s", pid.String())
	overlay := &OverlayConn{
		ID:             *pid,
		Reopen:         true,
		rendezvousAddr: rendezvousAddr,
		localAddr:      localAddr,
		peers:          make(SessionTable),
		peerDataChan:   make(chan []byte, 16),
	}
	overlay.createAutomata()
	overlay.automata.Event(eventOpen)
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
		[]Transition{
			Transition{src: stateClosed, event: eventOpen, dest: stateOpening},
			Transition{src: stateOpening, event: eventSuccess, dest: stateOpened},
			Transition{src: stateOpening, event: eventError, dest: stateClosed},
			Transition{src: stateOpened, event: eventClose, dest: stateClosed},
			Transition{src: stateOpened, event: eventBind, dest: stateBinding},
			Transition{src: stateBinding, event: eventSuccess, dest: stateListening},
			Transition{src: stateBinding, event: eventError, dest: stateBindError},
			Transition{src: stateBindError, event: eventUnderLimit, dest: stateOpened},
			Transition{src: stateBindError, event: eventOverLimit, dest: stateClosed},
			Transition{src: stateListening, event: eventClose, dest: stateClosed},
			Transition{src: stateListening, event: eventSuccess, dest: stateProcessingMessage},
			Transition{src: stateListening, event: eventError, dest: stateMessageError},
			Transition{src: stateListening, event: eventChannelExpired, dest: stateBinding},
			Transition{src: stateProcessingMessage, event: eventSuccess, dest: stateListening},
			Transition{src: stateProcessingMessage, event: eventError, dest: stateMessageError},
			Transition{src: stateMessageError, event: eventUnderLimit, dest: stateListening},
			Transition{src: stateMessageError, event: eventOverLimit, dest: stateBinding},
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
		overlay.automata.Event(eventOpen)
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
		overlay.automata.Event(eventError)
	} else {
		overlay.stun, err = stun.NewClient(
			stun.ClientOptions{
				Connection: overlay.conn,
			})
		if err != nil {
			log.Printf("failed dialing the STUN server at %s (backing off for %v) - %v",
				backoffDuration, overlay.rendezvousAddr, err)
			overlay.automata.Event(eventError)
		} else {
			log.Printf("local address: %s", overlay.conn.conn.LocalAddr().String())
			overlay.automata.Event(eventSuccess)
		}
	}
}

func (overlay *OverlayConn) opened([]interface{}) {
	overlay.automata.Event(eventBind)
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
			overlay.automata.Event(eventError)
		} else if e.Message == nil {
			log.Println("bindingError", errors.New("bindReq received an empty message"))
			overlay.automata.Event(eventError)
		} else if err := validateMessage(e.Message, &stun.BindingSuccess); err != nil {
			log.Println("bindingError", errors.Wrap(err, "bindReq received an invalid message:"))
			overlay.automata.Event(eventError)
		} else if err = overlay.externalAddr.GetFrom(e.Message); err != nil {
			log.Println("failed getting mapped address:", err)
			overlay.automata.Event(eventError)
		} else if err = overlay.bindPeerChannel(e.Message); err != nil {
			log.Println("failed processing session table:", err)
			overlay.automata.Event(eventError)
		} else {
			log.Println("XORMappedAddress", overlay.externalAddr)
			log.Println("LocalAddr", overlay.conn.conn.LocalAddr())
			log.Println("bindingSuccess")
			overlay.channelExpired = time.Now().Add(channelDuration)
			overlay.automata.Event(eventSuccess)
		}
	})

	if err = overlay.conn.conn.SetDeadline(deadline); err != nil {
		log.Println("failed setting connection read/write deadline")
		overlay.automata.Event(eventError)
	} else if msg, err = overlay.bindingRequestMessage(); err != nil {
		log.Println("failed building bindingRequestMessage", err)
		overlay.automata.Event(eventError)
	} else if err = overlay.stun.Start(msg, deadline, handler); err != nil {
		log.Println("binding failed:", err)
		overlay.automata.Event(eventError)
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
		overlay.automata.Event(eventOverLimit)
	} else {
		overlay.automata.Event(eventUnderLimit)
	}
}

func (overlay *OverlayConn) listening([]interface{}) {
	var (
		buf = make([]byte, readBufferSize)

		n    int
		addr *net.UDPAddr
		err  error
	)

	if err = overlay.conn.conn.SetDeadline(overlay.channelExpired); err != nil {
		log.Printf("failed to set read deadline: %v", err)
		overlay.automata.Event(eventError)
	} else if n, addr, err = overlay.conn.conn.ReadFromUDP(buf); err != nil {
		log.Printf("failed to read the message: %v", err)
		if time.Now().After(overlay.channelExpired) {
			overlay.automata.Event(eventChannelExpired)
		} else {
			overlay.automata.Event(eventError)
		}
	} else {
		overlay.channelExpired = time.Now().Add(channelDuration)
		overlay.msg, overlay.addr = buf[:n], addr
		overlay.automata.Event(eventSuccess)
	}
}

func (overlay *OverlayConn) parseHeader(req *stun.Message) (*PeerID, error) {
	if !stun.IsMessage(overlay.msg) {
		return nil, fmt.Errorf("!!! %s sent a message that is not a STUN message", overlay.addr)
	} else if _, err := req.Write(overlay.msg); err != nil {
		return nil, fmt.Errorf("failed to read message from %s: %v", overlay.addr, err)
	} else if err := validateMessage(req, nil); err != nil {
		return nil, fmt.Errorf("%s sent invalid STUN message: %v", overlay.addr, err)
	}

	pid := new(PeerID)
	if err := pid.GetFrom(req); err != nil {
		return nil, fmt.Errorf("failed to get peerID of %s: %v", overlay.addr, err)
	}
	return pid, nil
}

func (overlay *OverlayConn) processingMessage([]interface{}) {
	var (
		pid *PeerID
		req stun.Message
		err error
	)

	if pid, err = overlay.parseHeader(&req); err != nil {
		log.Println(err)
		overlay.automata.Event(eventError)
		return
	}

	err = nil
	switch req.Type {
	case stunBindingIndication:
		err = overlay.bindPeerChannel(&req)
	case stunDataRequest:
		err = overlay.peerDataRequest(pid, overlay.addr, &req)
	case stunChannelBindIndication:
		log.Printf("<- %s[%s] received channel bind indication", pid, overlay.addr)
	default:
		err = fmt.Errorf("!! %s[%s] sent a bad message - type:%v", pid, overlay.addr, req.Type)
	}

	if err == nil {
		overlay.automata.Event(eventSuccess)
	} else {
		log.Println(err)
		overlay.automata.Event(eventError)
	}
}

func (overlay *OverlayConn) peerDataRequest(pid *PeerID, addr *net.UDPAddr, req *stun.Message) error {
	// TODO: handle multi-packets payload
	var (
		data []byte
		err  error
	)

	if data, err = req.Get(stun.AttrData); err != nil {
		return fmt.Errorf("%s[%s] sent an invalid data request", pid, addr)
	}
	select {
	case overlay.peerDataChan <- data:
		return nil
	default:
		return fmt.Errorf("ERROR: peer data buffer is full")
	}
}

func (overlay *OverlayConn) bindPeerChannel(req *stun.Message) error {
	var (
		st   *SessionTable
		addr *net.UDPAddr
		msg  *stun.Message
		err  error
	)

	if st, err = GetSessionTableFrom(req); err != nil {
		return err
	}
	msg, err = stun.Build(
		stun.TransactionID,
		stunChannelBindIndication,
		&overlay.ID,
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
	if err != nil {
		return errors.Wrap(err, "failed creating channelBindIndication message")
	}
	for id, addrs := range *st {
		if id == overlay.ID {
			continue
		}
		if addr = addrs[0]; addr.IP.Equal(overlay.externalAddr.IP) {
			addr = addrs[1]
		}
		if err == nil {
			_, err = overlay.conn.conn.WriteToUDP(msg.Raw, addr)
		}
		if err != nil {
			log.Printf("WARNING: failed binding channel to %s[%s][%s] - %v",
				id, addrs[0].String(), addrs[1].String(), err)
		} else {
			overlay.peers[id] = addrs
			log.Printf("-> sent channelBind request to %s[%s][%s] ",
				id, addrs[0].String(), addrs[1].String())
		}
	}
	return nil
}

func (overlay *OverlayConn) buildDataErrorMessage(req, res *stun.Message, ec stun.ErrorCode) error {
	return res.Build(
		stun.NewTransactionIDSetter(req.TransactionID),
		stunDataError,
		ec,
		&overlay.ID,
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
}

func (overlay *OverlayConn) buildDataSuccessMessage(req, res *stun.Message) error {
	return res.Build(
		stun.NewTransactionIDSetter(req.TransactionID),
		stunDataSuccess,
		&overlay.ID,
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
}

func (overlay *OverlayConn) messageError([]interface{}) {
	overlay.errCount++
	if overlay.errCount >= listenErrorsLimit {
		overlay.errCount = 0
		overlay.automata.Event(eventOverLimit)
	} else {
		overlay.automata.Event(eventUnderLimit)
	}
}

// Read reads a multicast message sent by other
func (overlay *OverlayConn) Read(b []byte) (int, error) {
	if b == nil {
		return 0, fmt.Errorf("given buffer 'b' is nil")
	}

	var (
		data     []byte
		deadline = overlay.readDeadline
	)

	if deadline == nil {
		data = <-overlay.peerDataChan
	} else {
		select {
		case data = <-overlay.peerDataChan:
		case <-time.After(deadline.Sub(time.Now())):
		}
	}
	if len(data) > len(b) {
		return copy(b, data),
			fmt.Errorf("data (%d bytes) is not fit on given buffer 'b'", len(data))
	}
	return copy(b, data), nil
}

// Write sends a multicast message to other nodes
// TODO: handle multi-packets payload
func (overlay *OverlayConn) Write(b []byte) (int, error) {
	if len(b) > maxPacketDataSize {
		return 0, fmt.Errorf("data is too large, maximum %d bytes", maxPacketDataSize)
	}

	for {
		if overlay.writeDeadline != nil && overlay.writeDeadline.After(time.Now()) {
			return 0, fmt.Errorf("write timeout")
		}
		switch overlay.automata.Current() {
		case stateListening, stateProcessingMessage:
			return overlay.multicastMessage(b)
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func (overlay *OverlayConn) multicastMessage(data PeerData) (int, error) {
	var (
		msg  *stun.Message
		addr *net.UDPAddr
		err  error
	)

	msg, err = stun.Build(
		stun.TransactionID,
		stunDataRequest,
		data,
		&overlay.ID,
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
	if err != nil {
		return 0, errors.Wrap(err, "failed create data request message")
	}

	for id, addrs := range overlay.peers {
		if id == overlay.ID {
			continue
		}
		if addr = addrs[0]; addr.IP.Equal(overlay.externalAddr.IP) {
			addr = addrs[1]
		}
		if err == nil {
			_, err = overlay.conn.conn.WriteToUDP(msg.Raw, addr)
		}
		if err != nil {
			log.Printf("WARNING: failed sending data request to %s[%s][%s] - %v",
				id, addrs[0].String(), addrs[1].String(), err)
		} else {
			overlay.peers[id] = addrs
			log.Printf("-> sent data request to %s[%s][%s] ",
				id, addrs[0].String(), addrs[1].String())
		}
	}
	return 0, nil
}

// Close closes the overlay.
func (overlay *OverlayConn) Close() error {
	return overlay.automata.Event(eventClose)
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
