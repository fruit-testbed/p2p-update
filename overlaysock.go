// Copyright 2018 University of Glasgow.
// Use of this source code is governed by an Apache
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/gortc/stun"
	"github.com/pkg/errors"
)

var (
	errConnNotOpened = errors.New("connection is not opened")
	errNotReady      = errors.New("overlay is not ready")
	errBufferFull    = errors.New("data buffer is full")
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
		return -1, errConnNotOpened
	}
	return oc.conn.Read(p)
}

func (oc *overlayUDPConn) Write(p []byte) (n int, err error) {
	if oc.conn == nil {
		return -1, errConnNotOpened
	}
	return oc.conn.WriteToUDP(p, oc.rendezvousAddr)
}

func (oc *overlayUDPConn) Close() error {
	if oc.conn == nil {
		return nil
	}
	return oc.conn.Close()
}

// OverlayConfig decribes the configurations of OverlayConn
type OverlayConfig struct {
	Address             string        `json:"address,omitempty"`
	Server              string        `json:"server,omitempty"`
	StunPassword        string        `json:"stun-password"`
	BindingWait         time.Duration `json:"binding-wait"`
	BindingMaxErrors    int           `json:"binding-max-errors"`
	ListeningWait       time.Duration `json:"listening-wait"`
	ListeningMaxErrors  int           `json:"listening-max-errors"`
	ListeningBufferSize int           `json:"listening-buffer-size"`
	ErrorBackoff        time.Duration `json:"error-backoff"`
	ChannelLifespan     time.Duration `json:"channel-lifespan"`

	torrentPorts TorrentPorts
}

// OverlayConn is an implementation of net.Conn interface for a overlay network
// that uses STUN punching hole technique to enable peer-to-peer communications
// for nodes behind NATs.
type OverlayConn struct {
	sync.RWMutex

	ID     PeerID
	Reopen bool
	Config *OverlayConfig

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

	quitKeepAlive chan struct{}
}

// NewOverlayConn creates an overlay peer-to-peer connection that implements STUN
// punching hole technique to directly communicate to peers behind NATs.
func NewOverlayConn(cfg OverlayConfig) (*OverlayConn, error) {
	var (
		server, local *net.UDPAddr
		pid           *PeerID
		err           error
	)

	j, _ := json.Marshal(cfg)
	log.Printf("creating overlayconn with config: %s", string(j))

	if pid, err = LocalPeerID(); err != nil {
		return nil, errors.Wrap(err, "failed to get local ID")
	}
	log.Printf("local peer ID: %s", pid.String())
	if server, err = net.ResolveUDPAddr("udp", cfg.Server); err != nil {
		return nil, fmt.Errorf("Cannot resolve server address: %v", err)
	}
	if local, err = net.ResolveUDPAddr("udp", cfg.Address); err != nil {
		return nil, fmt.Errorf("Cannot resolve local address: %v", err)
	}
	overlay := &OverlayConn{
		ID:             *pid,
		Reopen:         true,
		Config:         &cfg,
		rendezvousAddr: server,
		localAddr:      local,
		peers:          make(SessionTable),
		peerDataChan:   make(chan []byte, 16),
	}
	overlay.createAutomata()
	overlay.automata.Event(eventOpen)

	j, _ = json.Marshal(overlay.Config)
	log.Printf("created overlayconn with config: %s", string(j))

	msg, err := stun.Build(
		stun.TransactionID,
		stunChannelBindIndication,
		&overlay.ID,
		stun.NewShortTermIntegrity(overlay.Config.StunPassword),
		stun.Fingerprint,
	)
	if err != nil {
		return nil, err
	}

	overlay.quitKeepAlive = ExecEvery(
		time.Duration(cfg.ChannelLifespan)*time.Second,
		overlay.keepAlive(msg))

	return overlay, nil
}

func (overlay *OverlayConn) createAutomata() {
	overlay.automata = NewAutomata(
		stateClosed,
		[]Transition{
			Transition{Src: stateClosed, Event: eventOpen, Dest: stateOpening},
			Transition{Src: stateOpening, Event: eventSuccess, Dest: stateOpened},
			Transition{Src: stateOpening, Event: eventError, Dest: stateClosed},
			Transition{Src: stateOpened, Event: eventClose, Dest: stateClosed},
			Transition{Src: stateOpened, Event: eventBind, Dest: stateBinding},
			Transition{Src: stateBinding, Event: eventSuccess, Dest: stateListening},
			Transition{Src: stateBinding, Event: eventError, Dest: stateBindError},
			Transition{Src: stateBindError, Event: eventUnderLimit, Dest: stateOpened},
			Transition{Src: stateBindError, Event: eventOverLimit, Dest: stateClosed},
			Transition{Src: stateListening, Event: eventClose, Dest: stateClosed},
			Transition{Src: stateListening, Event: eventSuccess, Dest: stateProcessingMessage},
			Transition{Src: stateListening, Event: eventError, Dest: stateMessageError},
			Transition{Src: stateListening, Event: eventChannelExpired, Dest: stateBinding},
			Transition{Src: stateProcessingMessage, Event: eventSuccess, Dest: stateListening},
			Transition{Src: stateProcessingMessage, Event: eventError, Dest: stateMessageError},
			Transition{Src: stateMessageError, Event: eventUnderLimit, Dest: stateListening},
			Transition{Src: stateMessageError, Event: eventOverLimit, Dest: stateBinding},
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

	overlay.quitKeepAlive <- struct{}{}

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
			overlay.Config.ErrorBackoff*time.Second, err)
		time.Sleep(overlay.Config.ErrorBackoff * time.Second)
		overlay.automata.Event(eventError)
	} else {
		overlay.stun, err = stun.NewClient(
			stun.ClientOptions{
				Connection: overlay.conn,
			})
		if err != nil {
			log.Printf("failed dialing the STUN server at %s: %v", overlay.rendezvousAddr, err)
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

	deadline := time.Now().Add(overlay.Config.BindingWait * time.Second)

	handler := stun.HandlerFunc(func(e stun.Event) {
		if e.Error != nil {
			log.Println("bindingError", e.Error)
			overlay.automata.Event(eventError)
		} else if e.Message == nil {
			log.Println("bindingError", errors.New("bindReq received an empty message"))
			overlay.automata.Event(eventError)
		} else if err := validateMessage(e.Message, &stun.BindingSuccess, overlay.Config.StunPassword); err != nil {
			log.Println("bindingError", errors.Wrap(err, "bindReq received an invalid message:"))
			overlay.automata.Event(eventError)
		} else if err = overlay.externalAddr.GetFrom(e.Message); err != nil {
			log.Println("failed getting mapped address:", err)
			overlay.automata.Event(eventError)
		} else if err = overlay.updateSessionTable(e.Message); err != nil {
			log.Println("failed updating session table:", err)
			overlay.automata.Event(eventError)
		} else {
			log.Println("XORMappedAddress", overlay.externalAddr)
			log.Println("LocalAddr", overlay.conn.conn.LocalAddr())
			log.Println("bindingSuccess")
			overlay.channelExpired = time.Now().Add(overlay.Config.ChannelLifespan * time.Second)
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
		&overlay.Config.torrentPorts,
		&overlay.ID,
		stun.NewShortTermIntegrity(overlay.Config.StunPassword),
		stun.Fingerprint,
	)
}

func (overlay *OverlayConn) bindError([]interface{}) {
	overlay.errCount++
	if overlay.errCount >= overlay.Config.BindingMaxErrors {
		overlay.errCount = 0
		time.Sleep(overlay.Config.ErrorBackoff * time.Second)
		overlay.automata.Event(eventOverLimit)
	} else {
		overlay.automata.Event(eventUnderLimit)
	}
}

func (overlay *OverlayConn) listening([]interface{}) {
	var (
		buf = make([]byte, overlay.Config.ListeningBufferSize)

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
		overlay.channelExpired = time.Now().Add(overlay.Config.ChannelLifespan * time.Second)
		overlay.msg, overlay.addr = buf[:n], addr
		overlay.automata.Event(eventSuccess)
	}
}

func (overlay *OverlayConn) parseHeader(req *stun.Message) (*PeerID, error) {
	if !stun.IsMessage(overlay.msg) {
		return nil, fmt.Errorf("!!! %s sent a message that is not a STUN message", overlay.addr)
	} else if _, err := req.Write(overlay.msg); err != nil {
		return nil, fmt.Errorf("failed to read message from %s: %v", overlay.addr, err)
	} else if err := validateMessage(req, nil, overlay.Config.StunPassword); err != nil {
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

	err = fmt.Errorf("!! %s[%s] sent a bad message - type:%v", pid, overlay.addr, req.Type)
	switch req.Type.Method {
	case stun.MethodBinding:
		switch req.Type.Class {
		case stun.ClassSuccessResponse, stun.ClassIndication:
			err = overlay.updateSessionTable(&req)
		}
	case stun.MethodData:
		switch req.Type.Class {
		case stun.ClassIndication:
			err = overlay.peerDataIndication(pid, overlay.addr, &req)
		}
	case stun.MethodChannelBind:
		switch req.Type.Class {
		case stun.ClassIndication:
			log.Printf("<- %s[%s] received channel bind indication", pid, overlay.addr)
			err = nil
		}
	}

	if err == nil {
		overlay.automata.Event(eventSuccess)
	} else {
		log.Println(err)
		overlay.automata.Event(eventError)
	}
}

func (overlay *OverlayConn) peerDataIndication(pid *PeerID, addr *net.UDPAddr, req *stun.Message) error {
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
		return errBufferFull
	}
}

func (overlay *OverlayConn) updateSessionTable(req *stun.Message) error {
	st, err := GetSessionTableFrom(req)
	if err != nil {
		return errors.Wrap(err, "updateSessionTable - failed getting session table from message")
	}
	overlay.Lock()
	defer overlay.Unlock()
	for id, sess := range *st {
		overlay.peers[id] = sess
	}
	return nil
}

func (overlay *OverlayConn) keepAlive(msg *stun.Message) func() {
	return func() {
		log.Println("sending keep alive packet")
		// send to server
		if bindMsg, err := overlay.bindingRequestMessage(); err == nil {
			overlay.conn.conn.WriteToUDP(bindMsg.Raw, overlay.rendezvousAddr)
		}

		// send to peers
		state := overlay.automata.Current()
		switch state {
		case stateListening, stateProcessingMessage, stateMessageError:
			overlay.RLock()
			for id, addrs := range overlay.peers {
				if id == overlay.ID {
					continue
				}
				addr := addrs[0]
				if addr.IP.Equal(overlay.externalAddr.IP) {
					addr = addrs[1]
				}
				_, err := overlay.conn.conn.WriteToUDP(msg.Raw, addr)
				if err != nil {
					log.Printf("WARNING: failed binding channel to %s[%s][%s] - %v",
						id, addrs[0].String(), addrs[1].String(), err)
				}
			}
			overlay.RUnlock()
		default:
			log.Printf("overlay is at state %s", state.String())
		}
		log.Println("sent keep alive packet")
	}
}

func (overlay *OverlayConn) messageError([]interface{}) {
	overlay.errCount++
	if overlay.errCount >= overlay.Config.ListeningMaxErrors {
		overlay.errCount = 0
		overlay.automata.Event(eventOverLimit)
	} else {
		overlay.automata.Event(eventUnderLimit)
	}
}

// Ready returns true if the overlay connection is ready to read or write packets,
// otherwise false.
func (overlay *OverlayConn) Ready() bool {
	switch overlay.automata.current {
	case stateListening, stateProcessingMessage, stateMessageError:
		return true
	}
	return false
}

// ReadMsg returns a multicast message sent by other peer.
func (overlay *OverlayConn) ReadMsg() ([]byte, error) {
	if !overlay.Ready() {
		return nil, errNotReady
	}
	deadline := overlay.readDeadline
	if deadline == nil {
		return <-overlay.peerDataChan, nil
	}
	select {
	case data := <-overlay.peerDataChan:
		return data, nil
	case <-time.After(deadline.Sub(time.Now())):
	}
	return nil, errNotReady
}

// Read reads a multicast message sent by other
func (overlay *OverlayConn) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, fmt.Errorf("given buffer 'b' is nil")
	}
	if !overlay.Ready() {
		return 0, errNotReady
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
	if len(b) > stunMaxPacketDataSize {
		return 0, fmt.Errorf("data is too large, maximum %d bytes",
			stunMaxPacketDataSize)
	}

	// TODO: apply writeDeadline
	current := overlay.automata.Current()
	switch current {
	case stateListening, stateProcessingMessage:
		if _, err := overlay.multicastMessage(b); err != nil {
			return 0, err
		}
		return len(b), nil
	default:
		return 0, fmt.Errorf("connection (state: %d) is not ready", current)
	}
}

func (overlay *OverlayConn) multicastMessage(data PeerMessage) (int, error) {
	var (
		msg  *stun.Message
		addr *net.UDPAddr
		err  error
	)

	msg, err = stun.Build(
		stun.TransactionID,
		stunDataIndication,
		data,
		&overlay.ID,
		stun.NewShortTermIntegrity(overlay.Config.StunPassword),
		stun.Fingerprint,
	)
	if err != nil {
		return 0, errors.Wrap(err, "failed create data request message")
	}

	overlay.RLock()
	defer overlay.RUnlock()
	for id, addrs := range overlay.peers {
		if id == overlay.ID {
			continue
		}
		if addr = addrs[0]; addr.IP.Equal(overlay.externalAddr.IP) {
			addr = addrs[1]
		}
		if err == nil {
			_, err = overlay.conn.conn.WriteTo(msg.Raw, addr)
		}
		if err != nil {
			log.Printf("WARNING: failed sending data request to %s[%s][%s] - %v",
				id, addrs[0].String(), addrs[1].String(), err)
		} else {
			log.Printf("-> sent data request to %s[%s][%s] ",
				id, addrs[0].String(), addrs[1].String())
		}
	}
	return len(data), nil
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
