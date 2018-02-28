package main

import (
	"fmt"
	"log"
	"net"
	"time"

	"github.com/gortc/stun"
	"github.com/looplab/fsm"
	"github.com/pkg/errors"
)

type StunClient struct {
	ID     string
	client *stun.Client

	extIP   net.IP
	extPort int
	session SessionTable

	fsm  *fsm.FSM
	quit chan int
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
		quit: make(chan int, 1),
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
			"bind":        func(*fsm.Event) { sc.bind() },
			"stop":        func(*fsm.Event) { sc.reset() },
			"bindSuccess": noop,
			"bindError":   noop,
			"reset":       func(*fsm.Event) { sc.reset() },
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
	return sc.fsm.Event("bind")
}

func (sc *StunClient) keepAlive() {
	// Some applications send a keep-alive packet every 60 seconds. Here we set 30 seconds.
	// reference: https://stackoverflow.com/q/13501288
	log.Println("Started keep alive thread")
	for {
		select {
		case <-sc.quit:
			log.Println("Stopped keep alive thread")
			return
		case <-time.After(30 * time.Second):
			if sc.fsm.Current() == "registered" {
				sc.bind()
			}
		}
	}
}

func (sc *StunClient) bind() {
	deadline := time.Now().Add(stunReplyTimeout)
	handler := stun.HandlerFunc(func(e stun.Event) {
		var (
			xorAddr stun.XORMappedAddress
			st      SessionTable
		)
		if e.Error == stun.ErrTransactionTimeOut {
			sc.fsm.Event("bindError")
		} else if e.Error != nil {
			log.Println("Got error", e.Error)
		} else if e.Message == nil {
			log.Println("Empty message")
		} else if err := validateMessage(e.Message, &stun.BindingSuccess); err != nil {
			log.Println("Invalid response message:", err)
			sc.fsm.Event("bindError")
		} else if err = xorAddr.GetFrom(e.Message); err != nil {
			log.Println("Failed getting mapped address:", err)
		} else if st, err = getSessionTable(e.Message); err != nil {
			log.Println("Failed extracting session-table:", err, e.Message)
		} else {
			delete(st, sc.ID)
			sc.extIP, sc.extPort, sc.session = xorAddr.IP, xorAddr.Port, st
			log.Println("mapped-address:", xorAddr, "- session-table:", st)
			sc.fsm.Event("bindSuccess")
			return
		}
		sc.fsm.Event("bindError")
	})
	if err := sc.client.Start(sc.bindMessage(), deadline, handler); err != nil {
		log.Printf("Binding failed: %v", err)
		sc.fsm.Event("bindError")
	}
}

func (sc *StunClient) reset() {
	sc.quit <- 1
	sc.extIP = nil
	sc.extPort = 0
	sc.session = nil
}

func noop(*fsm.Event) {}

func (sc *StunClient) Stop() error {
	return sc.fsm.Event("stop")
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
