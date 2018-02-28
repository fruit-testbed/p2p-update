package main

import (
	"fmt"
	"log"
	"time"

	"github.com/gortc/stun"
	"github.com/looplab/fsm"
	"github.com/pkg/errors"
)

type StunClient struct {
	ID     string
	client *stun.Client
	fsm    *fsm.FSM
	quit   chan int
}

func NewStunClient() (*StunClient, error) {
	var (
		id  string
		err error
	)
	if id, err = localID(); err != nil {
		return nil, errors.Wrap(err, "Cannot get local id")
	}
	sc := &StunClient{
		ID:   id,
		quit: make(chan int, 2),
	}
	sc.createFSM()
	return sc, nil
}

func (sc *StunClient) createFSM() {
	sc.fsm = fsm.NewFSM(
		"stopped",
		fsm.Events{
			{Name: "bind", Src: []string{"stopped", "registering"}, Dst: "registering"},
			{Name: "stop", Src: []string{"stopped", "registering", "registered", "connected"}, Dst: "stopped"},
			{Name: "bindSuccess", Src: []string{"registering"}, Dst: "registered"},
			{Name: "bindError", Src: []string{"registering"}, Dst: "notRegistered"},
			{Name: "reset", Src: []string{"notRegistered"}, Dst: "stopped"},
			{Name: "peerData", Src: []string{"registered", "connected"}, Dst: "connected"},
			{Name: "noPeerData", Src: []string{"connected"}, Dst: "registered"},
		},
		fsm.Callbacks{
			"bind":        sc.callbackBind,
			"stop":        noop,
			"bindSuccess": noop,
			"bindError":   sc.callbackBindError,
			"reset":       noop,
			"peerData":    noop,
			"noPeerData":  noop,
		},
	)
}

func (sc *StunClient) Start(address string) error {
	if sc.fsm.Current() != "stopped" {
		return errors.New("StunClient has been started")
	}
	var err error
	if sc.client, err = stun.Dial("udp", address); err != nil {
		return errors.Wrap(err, fmt.Sprintf("Failed dialing the server: %v", err))
	}
	go sc.keepAlive()
	go sc.refreshSessionTable()
	return sc.fsm.Event("bind")
}

func (sc *StunClient) refreshSessionTable() {
	log.Println("Started refreshSessionTable thread")
	for {
		select {
		case <-sc.quit:
			log.Println("Stopped refreshSessionTable thread")
		case <-time.After(30 * time.Second):
			sc.sendRefreshSessionTableRequest()
		}
	}
}

func (sc *StunClient) sendRefreshSessionTableRequest() {
	deadline := time.Now().Add(stunReplyTimeout)
	handler := stun.HandlerFunc(func(e stun.Event) {
		msgType := stun.NewType(stun.MethodRefresh, stun.ClassSuccessResponse)
		if e.Error != nil {
			log.Println("Failed sent refreshSessionTable request to STUN server:", e.Error)
		} else if e.Message == nil {
			log.Println("Received an empty message")
		} else if err := validateMessage(e.Message, &msgType); err != nil {
			log.Println("Failed sent keep-alive packet to STUN server: invalid message:", err)
		} else {
			// TODO: extract server's session-table then save it locally
			st, err := getSessionTable(e.Message)
			if err == nil {
				log.Println("Got session table:", st)
			} else {
				log.Println("Failed extracting session-table:", err, e.Message)
			}
		}
	})
	if err := sc.client.Start(sc.refreshMessage(), deadline, handler); err != nil {
		log.Println("sendRefreshSessionTableRequest failed:", err)
		sc.fsm.Event("bindError")
	}
}

func (sc *StunClient) keepAlive() {
	// Some applications send a keep-alive packet every 60 seconds. Here we set 30 seconds.
	// reference: https://stackoverflow.com/q/13501288
	stunKeepAliveTimeout := 30 // in seconds
	counter := 0
	log.Println("Started keep alive thread")
	for {
		select {
		case <-sc.quit:
			log.Println("Stopped keep alive thread")
			return
		case <-time.After(time.Second):
			if sc.fsm.Current() != "registered" {
				counter = 0
			} else if counter++; counter > stunKeepAliveTimeout {
				sc.sendKeepAliveMessage()
				counter = 0
			}
		}
	}
}

func (sc *StunClient) sendKeepAliveMessage() {
	deadline := time.Now().Add(stunReplyTimeout)
	handler := stun.HandlerFunc(func(e stun.Event) {
		if e.Error != nil {
			log.Println("Failed sent keep-alive packet to STUN server:", e.Error)
		} else if e.Message == nil {
			log.Println("Failed sent keep-alive packet to STUN server: empty message")
		} else if err := validateMessage(e.Message, &stun.BindingSuccess); err != nil {
			log.Println("Failed sent keep-alive packet to STUN server: invalid message -", err)
		}
	})
	if err := sc.client.Start(sc.bindMessage(), deadline, handler); err != nil {
		log.Println("Binding failed:", err)
		sc.fsm.Event("bindError")
	}
}

func (sc *StunClient) callbackBind(*fsm.Event) {
	deadline := time.Now().Add(stunReplyTimeout)
	handler := stun.HandlerFunc(func(e stun.Event) {
		if e.Error == stun.ErrTransactionTimeOut {
			sc.fsm.Event("bindError")
		} else if e.Error != nil {
			log.Println("Got error", e.Error)
		} else if e.Message == nil {
			log.Println("Empty message")
		} else if err := validateMessage(e.Message, &stun.BindingSuccess); err != nil {
			log.Println("Invalid response message:", err)
			sc.fsm.Event("bindError")
		} else {
			var xorAddr stun.XORMappedAddress
			if err = xorAddr.GetFrom(e.Message); err != nil {
				log.Println("Failed getting mapped address:", err)
			} else {
				log.Println("Mapped address", xorAddr)
			}
			sc.fsm.Event("bindSuccess")
		}
	})
	if err := sc.client.Start(sc.bindMessage(), deadline, handler); err != nil {
		log.Printf("Binding failed: %v", err)
		sc.fsm.Event("bindError")
	}
}

func (sc *StunClient) callbackBindError(*fsm.Event) {
	sc.fsm.Event("reset")
}

func noop(*fsm.Event) {}

func (sc *StunClient) Stop() error {
	if err := sc.fsm.Event("stop"); err != nil {
		return err
	}
	sc.quit <- 1
	return nil
}

func (sc *StunClient) bindMessage() *stun.Message {
	return stun.MustBuild(
		stun.TransactionID,
		stun.NewType(stun.MethodBinding, stun.ClassRequest),
		stunSoftware,
		stun.NewUsername(sc.ID),
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
}

func (sc *StunClient) refreshMessage() *stun.Message {
	return stun.MustBuild(
		stun.TransactionID,
		stun.NewType(stun.MethodRefresh, stun.ClassRequest),
		stunSoftware,
		stun.NewUsername(sc.ID),
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
}
