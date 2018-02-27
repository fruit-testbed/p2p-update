package main

import (
	"time"
	"log"
	"fmt"

	"github.com/gortc/stun"
	"github.com/pkg/errors"
)

type StunClient struct {
	Id string
	Client *stun.Client
}

func NewStunClient() (*StunClient, error) {
	var id string
	var err error
	//var conn net.Conn
	if id, err = localId(); err != nil {
		return nil, errors.Wrap(err, "Cannot get local id")
	}
	//conn, err = net.Dial("udp", nil)
	return &StunClient {
		Id: id,
	}, nil
}

func (c *StunClient) NewStunSession() *StunSession {
	return &StunSession {
		Client: c,
		Peer: nil,
		Alive: false,
		Errors: make([]*error, 0, stunSessionMaxErrors),
		Deadline: stunWaitReplyDeadline,
	}
}

type StunSession struct {
	Client *StunClient
	Peer *Peer
	Alive bool
	Errors []*error
	Deadline time.Time
}

func (ss *StunSession) reset() {
	ss.clearErrors()
	ss.Alive = false
	ss.Peer = nil
}

func (ss *StunSession) clearErrors() {
	for i := range ss.Errors {
		ss.Errors[i] = nil
	}
	ss.Errors = ss.Errors[:0]
}

func (ss *StunSession) addError(err *error) bool {
	log.Println(fmt.Sprintf("WARNING: STUN session error: %v", *err))
	if len(ss.Errors) < cap(ss.Errors) {
		ss.Errors = append(ss.Errors, err)
	}
	return len(ss.Errors) >= cap(ss.Errors)
}

func (ss *StunSession) Start(address string) error {
	if ss.Alive {
		return errors.New("Cannot start a session that is alive")
	}
	ss.reset()
	ss.Alive = true
	fsuccess := func(res *stun.Event) {
		log.Println("Ping STUN server was successful. Dialing STUN server.")
		time.Sleep(1000 * time.Millisecond)
		m := stun.MustBuild(
			stun.TransactionID,
			stun.NewType(stun.MethodRefresh, stun.ClassRequest),
			stunSoftware,
			stun.NewUsername(ss.Client.Id),
			stun.NewShortTermIntegrity(stunPassword),
			stun.Fingerprint,
		)
		if err := ss.Client.Client.Do(m, ss.Deadline, ss.callback(noResponse, noResponse)); err != nil {
			log.Printf(fmt.Sprintf("Failed to dial the server: %v", err))
		}
	}
	ferror := func(res *stun.Event) {
		log.Println("WARNING: Cannot ping STUN server at %s", *stunServerAddrConnect)
		ss.Alive = false
	}

	var err error
	if ss.Client.Client == nil {
		if ss.Client.Client, err = stun.Dial("udp", address); err != nil {
			return errors.Wrap(err, "Failed to dial the server")
		}
	}

	m := stun.MustBuild(
		stun.TransactionID,
		stun.NewType(stun.MethodRefresh, stun.ClassRequest),
		stunSoftware,
		stun.NewUsername(ss.Client.Id),
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
	if err := ss.Client.Client.Do(m, ss.Deadline, ss.callback(fsuccess, ferror)); err != nil {
		return errors.Wrap(err, "Failed to dial the server")
	}
	return nil
}

func (ss *StunSession) callback(fsuccess func(*stun.Event), ferror func(*stun.Event)) func(stun.Event) {
	return func(res stun.Event) {
		if res.Error != nil && !ss.addError(&res.Error) {
			return
		}
		err := ValidateMessage(res.Message, &stunTypeRefreshSuccess)
		if err == nil {
			ss.clearErrors()
			fsuccess(&res)
		} else if ss.addError(&err) {
			ferror(&res)
		} else {
			// stop the session due to too many errors
			ss.Alive = false
		}
	}
}

func noResponse(*stun.Event) {}
