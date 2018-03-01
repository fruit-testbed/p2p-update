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

type automata struct {
	sync.Mutex
	current     int
	transitions transitions // src->(event->dest)
	callbacks   callbacks   // dest->callback
}

type transition struct {
	src   int
	event int
	dest  int
}

type transitions map[int]map[int]int

type callbacks map[int]func()

func NewAutomata(current int, ts []transition, callbacks callbacks) *automata {
	fsm := &automata{
		current:     current,
		transitions: make(transitions),
		callbacks:   callbacks,
	}
	for i := range ts {
		if _, ok := fsm.transitions[ts[i].src]; !ok {
			fsm.transitions[ts[i].src] = make(map[int]int, 0)
		}
		fsm.transitions[ts[i].src][ts[i].event] = ts[i].dest
	}
	return fsm
}

func (a *automata) event(event int) error {
	var (
		dest int
		ok   bool
		cb   func()
	)
	if dest, ok = a.transitions[a.current][event]; ok {
		a.Lock()
		log.Println("event", event, "transition from", a.current, "to", dest)
		a.current = dest
		a.Unlock()
		if cb, ok = a.callbacks[a.current]; ok {
			cb()
		}
		return nil
	}
	return fmt.Errorf("state %d does not have transition for event %d", a.current, event)
}

type Overlay struct {
	sync.Mutex
	ID       string
	stun     *stun.Client
	automata *automata
	peer     Peer
	conn     net.Conn
	errCount int
}

func NewOverlay(id string) *Overlay {
	overlay := &Overlay{
		ID: id,
	}
	overlay.createAutomata()
	return overlay
}

const (
	stateStopped = iota
	stateBinding
	stateBindError
	stateReady
	stateWaitPeer
	stateChannelBindError
	statePeerConnected
	stateWaitData
	stateDataError
	stateBadPeerConnection
	stateClosed
)

const (
	eventBindReq = iota + 100
	eventBindingSuccess
	eventBindingError
	eventStop
	eventChannelBindReq
	eventWaitPeerError
	eventErrorsUnderLimit
	eventErrorsAboveLimit
	eventPeerConnect
	eventDataReq
	eventDataResp
	eventWaitDataError
	eventClose
)

func (overlay *Overlay) createAutomata() {
	overlay.automata = NewAutomata(
		stateStopped,
		[]transition{
			transition{src: stateStopped, event: eventStop, dest: stateStopped},
			transition{src: stateStopped, event: eventBindReq, dest: stateBinding},
			transition{src: stateBinding, event: eventBindingError, dest: stateBindError},
			transition{src: stateBindError, event: eventStop, dest: stateStopped},
			transition{src: stateBinding, event: eventBindingSuccess, dest: stateReady},
			transition{src: stateReady, event: eventStop, dest: stateStopped},
			transition{src: stateReady, event: eventChannelBindReq, dest: stateWaitPeer},
			transition{src: stateWaitPeer, event: eventWaitPeerError, dest: stateChannelBindError},
			transition{src: stateChannelBindError, event: eventErrorsUnderLimit, dest: stateReady},
			transition{src: stateChannelBindError, event: eventErrorsAboveLimit, dest: stateStopped},
			transition{src: stateWaitPeer, event: eventPeerConnect, dest: statePeerConnected},
			transition{src: statePeerConnected, event: eventDataReq, dest: stateWaitData},
			transition{src: statePeerConnected, event: eventClose, dest: stateClosed},
			transition{src: stateWaitData, event: eventDataResp, dest: statePeerConnected},
			transition{src: stateWaitData, event: eventWaitDataError, dest: stateDataError},
			transition{src: stateDataError, event: eventErrorsUnderLimit, dest: statePeerConnected},
			transition{src: stateDataError, event: eventErrorsAboveLimit, dest: stateBadPeerConnection},
			transition{src: stateBadPeerConnection, event: eventClose, dest: stateClosed},
		},
		callbacks{
			stateBinding:          overlay.binding,
			stateBindError:        overlay.bindError,
			stateStopped:          overlay.stopped,
			stateReady:            func() { overlay.automata.event(eventChannelBindReq) },
			stateWaitPeer:         overlay.waitPeer,
			stateChannelBindError: overlay.channelBindError,
		},
	)
}

func (overlay *Overlay) Open(serverAddr string) error {
	var err error
	overlay.conn, err = net.Dial("udp", serverAddr)
	if err != nil {
		return err
	}
	overlay.stun, err = stun.NewClient(stun.ClientOptions{
		Connection: overlay.conn,
	})
	if err != nil {
		return errors.Wrapf(err, "Failed dialing the STUN server at %s", serverAddr)
	}
	return overlay.automata.event(eventBindReq)
}

func (overlay *Overlay) stopped() {
	overlay.Lock()
	overlay.errCount = 0
	overlay.Unlock()
	overlay.automata.event(eventBindReq)
}

func (overlay *Overlay) channelBindError() {
	overlay.Lock()
	overlay.errCount++
	overlay.Unlock()
	if overlay.errCount >= 2 {
		overlay.automata.event(eventErrorsAboveLimit)
	} else {
		overlay.automata.event(eventErrorsUnderLimit)
	}
}

func (overlay *Overlay) bindError() {
	select {
	case <-time.After(time.Second):
		if err := overlay.automata.event(eventStop); err != nil {
			log.Println("raise event stop failed:", err)
		}
	}
}

func (overlay *Overlay) waitPeer() {
	deadline := time.Now().Add(1 * time.Second)
	handler := stun.HandlerFunc(func(e stun.Event) {
		if e.Error != nil {
			log.Println("channelBindReq error", e.Error)
		} else if e.Message == nil {
			log.Println("channelBindReq received an empty message")
		} else if err := validateMessage(e.Message, &stun.BindingSuccess); err != nil {
			log.Println("channelBindReq received an invalid message:", err)
		} else {
			// TODO: extract peer's IP/port
			overlay.automata.event(eventPeerConnect)
			return
		}
		overlay.automata.event(eventWaitPeerError)
	})
	if err := overlay.stun.Start(overlay.channelBindMessage(), deadline, handler); err != nil {
		log.Println("channelBindReq failed:", err)
		overlay.automata.event(eventWaitPeerError)
	}
}

func (overlay *Overlay) channelBindMessage() *stun.Message {
	return stun.MustBuild(
		stun.TransactionID,
		stun.NewType(stun.MethodChannelBind, stun.ClassRequest),
		stunSoftware,
		stun.NewUsername(overlay.ID),
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
}

func (overlay *Overlay) binding() {
	deadline := time.Now().Add(10 * time.Second)
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
			log.Println("LocalAddr", overlay.conn.LocalAddr())
			log.Println("RemoteAddr", overlay.conn.RemoteAddr())
			log.Println("bindingSuccess")
			overlay.automata.event(eventBindingSuccess)
		}
		overlay.automata.event(eventBindingError)
	})
	if err := overlay.stun.Start(overlay.bindMessage(), deadline, handler); err != nil {
		log.Println("binding failed:", err)
		overlay.automata.event(eventBindingError)
	}
}

func (overlay *Overlay) bindMessage() *stun.Message {
	return stun.MustBuild(
		stun.TransactionID,
		stun.BindingRequest,
		stunSoftware,
		stun.NewUsername(overlay.ID),
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
}
