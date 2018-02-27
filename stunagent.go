package main

import (
	"time"
	"log"

	"github.com/gortc/stun"
	"github.com/pkg/errors"
)

type StunAgent struct {
	Id string
	Client *stun.Client
	Peer *Peer
	Alive bool
	Errors []*error
	Deadline time.Time
}

func NewStunAgent() (*StunAgent, error) {
	var id string
	var err error
	if id, err = localId(); err != nil {
		return nil, errors.Wrap(err, "Cannot get local id")
	}
	return &StunAgent {
		Id: id,
	}, nil
}

func (sa *StunAgent) reset() {
	sa.clearErrors()
	sa.Alive = false
	sa.Peer = nil
}

func (sa *StunAgent) clearErrors() {
	for i := range sa.Errors {
		sa.Errors[i] = nil
	}
	sa.Errors = sa.Errors[:0]
}

func (sa *StunAgent) addError(err *error) bool {
	log.Printf("WARNING: STUN session error: %v", *err)
	if len(sa.Errors) < cap(sa.Errors) {
		sa.Errors = append(sa.Errors, err)
	}
	return len(sa.Errors) >= cap(sa.Errors)
}

func (sa *StunAgent) Start(address string) error {
	if sa.Alive {
		return errors.New("Cannot start a session that is alive")
	}
	sa.reset()
	sa.Alive = true
	fsuccess := func(res *stun.Event) {
		log.Println("Ping STUN server was successful. Dialing STUN server.")
		time.Sleep(1000 * time.Millisecond)
		if err := sa.Client.Do(sa.pingMessage(), sa.Deadline, sa.callback(noResponse, noResponse)); err != nil {
			log.Printf("Failed to dial the server: %v", err)
		}
	}
	ferror := func(res *stun.Event) {
		log.Println("WARNING: Cannot ping STUN server at %s", *stunServerAddrConnect)
		sa.Alive = false
	}

	var err error
	if sa.Client == nil {
		if sa.Client, err = stun.Dial("udp", address); err != nil {
			return errors.Wrap(err, "Failed to dial the server")
		}
	}

	if err := sa.Client.Do(sa.pingMessage(), sa.Deadline, sa.callback(fsuccess, ferror)); err != nil {
		return errors.Wrap(err, "Failed to dial the server")
	}
	return nil
}

func (sa *StunAgent) pingMessage() *stun.Message {
	return stun.MustBuild(
		stun.TransactionID,
		stun.NewType(stun.MethodRefresh, stun.ClassRequest),
		stunSoftware,
		stun.NewUsername(sa.Id),
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
}

func (sa *StunAgent) callback(fsuccess func(*stun.Event), ferror func(*stun.Event)) func(stun.Event) {
	return func(res stun.Event) {
		if res.Error != nil && !sa.addError(&res.Error) {
			return
		}
		err := ValidateMessage(res.Message, &stunTypeRefreshSuccess)
		if err == nil {
			sa.clearErrors()
			fsuccess(&res)
		} else if sa.addError(&err) {
			ferror(&res)
		} else {
			// stop the session due to too many errors
			sa.Alive = false
		}
	}
}

func noResponse(*stun.Event) {}
