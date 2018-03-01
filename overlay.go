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

type Overlay struct {
	sync.Mutex
	ID             string
	automata       *automata
	rendezvousAddr *net.UDPAddr
	stun           *stun.Client
	conn           *net.UDPConn
	peer           Peer
	errCount       int
	dataHandler    DataHandler

	res *stun.Message
	req *stun.Message
}

type DataHandler interface {
	HandleData([]byte) error
}

func NewOverlay(id string, rendezvousAddr string, dataHandler DataHandler) (*Overlay, error) {
	var (
		addr *net.UDPAddr
		err  error
	)
	if addr, err = net.ResolveUDPAddr("udp", rendezvousAddr); err != nil {
		return nil, errors.Wrapf(err, "failed resolving rendezvous address %s", rendezvousAddr)
	}
	overlay := &Overlay{
		ID:             id,
		rendezvousAddr: addr,
		dataHandler:    dataHandler,
		req:            new(stun.Message),
		res:            new(stun.Message),
	}
	overlay.createAutomata()
	return overlay, nil
}

const (
	stateStopped = iota
	stateBinding
	stateBindError
	stateReady
	stateWaitingPeer
	stateChannelBindError
	stateReceivingPeerData
	stateWaitingPeerData
	stateDataError
	stateBadChannel
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
			transition{src: stateReady, event: eventChannelBindReq, dest: stateWaitingPeer},
			transition{src: stateWaitingPeer, event: eventWaitPeerError, dest: stateChannelBindError},
			transition{src: stateChannelBindError, event: eventErrorsUnderLimit, dest: stateReady},
			transition{src: stateChannelBindError, event: eventErrorsAboveLimit, dest: stateStopped},
			transition{src: stateWaitingPeer, event: eventPeerConnect, dest: stateReceivingPeerData},
			transition{src: stateReceivingPeerData, event: eventDataReq, dest: stateWaitingPeerData},
			transition{src: stateReceivingPeerData, event: eventClose, dest: stateClosed},
			transition{src: stateWaitingPeerData, event: eventDataResp, dest: stateReceivingPeerData},
			transition{src: stateWaitingPeerData, event: eventWaitDataError, dest: stateDataError},
			transition{src: stateDataError, event: eventErrorsUnderLimit, dest: stateReceivingPeerData},
			transition{src: stateDataError, event: eventErrorsAboveLimit, dest: stateBadChannel},
			transition{src: stateBadChannel, event: eventClose, dest: stateClosed},
		},
		callbacks{
			stateBinding:           overlay.binding,
			stateBindError:         overlay.bindError,
			stateStopped:           overlay.stopped,
			stateReady:             overlay.ready,
			stateWaitingPeer:       overlay.waitingPeer,
			stateChannelBindError:  overlay.channelBindError,
			stateReceivingPeerData: overlay.receivingPeerData,
			stateWaitingPeerData:   overlay.waitingPeer,
		},
	)
}

func (overlay *Overlay) Open() error {
	var err error
	if overlay.conn, err = net.ListenUDP("udp", nil); err != nil {
		return errors.Wrap(err, "failed creating UDP connection")
	}
	overlay.stun, err = stun.NewClient(stun.ClientOptions{
		Connection: overlay,
	})
	if err != nil {
		return errors.Wrapf(err, "Failed dialing the STUN server at %s", overlay.rendezvousAddr)
	}
	return overlay.automata.event(eventBindReq)
}

func (overlay *Overlay) Read(p []byte) (n int, err error) {
	return overlay.conn.Read(p)
}

func (overlay *Overlay) Write(p []byte) (n int, err error) {
	return overlay.conn.WriteToUDP(p, overlay.rendezvousAddr)
}

func (overlay *Overlay) Close() error {
	return overlay.conn.Close()
}

func (overlay *Overlay) receivingPeerData() {
	// TODO
	overlay.automata.event(eventDataReq)
}

func (overlay *Overlay) ready() {
	// TODO: send a keep-alive packet to STUN server
	overlay.automata.event(eventChannelBindReq)
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

func (overlay *Overlay) processMessage(addr net.Addr, msg []byte, req *stun.Message) error {
	switch peer := addr.(type) {
	case *net.UDPAddr:
		overlay.peer = Peer{IP: peer.IP, Port: peer.Port}
		log.Printf("received STUN message from %v:%d", peer.IP, peer.Port)
	default:
		return fmt.Errorf("unknown addr: %v", addr)
	}

	// Convert the packet to STUN message
	var peerID stun.Username
	if _, err := req.Write(msg); err != nil {
		return errors.Wrap(err, "failed to read message")
	} else if err := validateMessage(req, &stunDataRequest); err != nil {
		return errors.Wrap(err, "invalid STUN message")
	} else if err := peerID.GetFrom(req); err != nil {
		return errors.Wrap(err, "failed to get peerID")
	} else if req.Contains(stun.AttrData) {
		if data, err := req.Get(stun.AttrData); err != nil {
			return errors.Wrap(err, "failed get the data from STUN message")
		} else if overlay.dataHandler != nil {
			return overlay.dataHandler.HandleData(data)
		}
	}
	overlay.peer.ID = peerID.String()
	return nil
}

func (overlay *Overlay) waitingPeer() {
	var (
		deadline = time.Now().Add(30 * time.Second)
		buf      = make([]byte, 64*1024)

		respType stun.MessageType
		n        int
		addr     net.Addr
		err      error
	)

	if err = overlay.conn.SetReadDeadline(deadline); err != nil {
		err = fmt.Errorf("failed to set read deadline: %v", err)
	} else if n, addr, err = overlay.conn.ReadFrom(buf); err != nil {
		err = fmt.Errorf("failed to read STUN message: %v", err)
	} else if !stun.IsMessage(buf[:n]) {
		err = fmt.Errorf("received not a STUN message")
	} else if err = overlay.processMessage(addr, buf[:n], overlay.req); err != nil {
		err = fmt.Errorf("failed processing the message")
	}

	if err != nil {
		respType = stunDataError
	} else {
		respType = stunDataSuccess
	}
	overlay.res.Build(
		stun.NewTransactionIDSetter(overlay.req.TransactionID),
		respType,
		stunSoftware,
		stun.NewUsername(overlay.ID),
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
	_, err = overlay.conn.WriteTo(overlay.res.Raw, addr)
	if err != nil {
		err = fmt.Errorf("failed WriteTo %v - %v", addr, err)
	}
	overlay.req.Reset()
	overlay.res.Reset()

	if err != nil {
		overlay.automata.event(eventWaitPeerError)
	} else {
		overlay.automata.event(eventPeerConnect)
	}
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
	if err := overlay.stun.Start(overlay.bindingRequestMessage(), deadline, handler); err != nil {
		log.Println("binding failed:", err)
		overlay.automata.event(eventBindingError)
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
