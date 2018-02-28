package main

import (
	"fmt"
	"log"
	"time"

	"github.com/gortc/stun"
	"github.com/pkg/errors"
)

type StunState uint16

const (
	StunStateStopped            StunState = 0x000
	StunStateRegistering        StunState = 0x001
	StunStateRegistered         StunState = 0x002
	StunStateRegistrationFailed StunState = 0x003
	StunStateConnected          StunState = 0x004
)

const (
	StunTransitionBinding     = 1
	StunTransitionBindSuccess = 2
	StunTransitionBindError   = 3
	StunTransitionReset       = 4
	StunTransitionStop        = 5
	StunTransitionPeer        = 6
	StunTransitionNoPeer      = 7
)

type StunClient struct {
	ID     string
	client *stun.Client
	State  StunState
	fsm    chan int
}

func NewStunClient() (*StunClient, error) {
	var (
		id  string
		err error
	)
	if id, err = localID(); err != nil {
		return nil, errors.Wrap(err, "Cannot get local id")
	}
	return &StunClient{
		ID:    id,
		State: StunStateStopped,
		fsm:   make(chan int, 1),
	}, nil
}

func (sc *StunClient) Start(address string) error {
	if sc.State != StunStateStopped {
		return errors.New("StunClient has been started")
	}
	var err error
	if sc.client, err = stun.Dial("udp", address); err != nil {
		return errors.Wrap(err, fmt.Sprintf("Failed dialing the server: %v", err))
	}
	go func() {
		for {
			msg := <-sc.fsm
			sc.transition(msg)
		}
	}()
	go sc.keepAlive()
	sc.fsm <- StunTransitionBinding
	return nil
}

func (sc *StunClient) keepAlive() {
	// Some applications send a keep-alive packet every 60 seconds. Here we set 30 seconds.
	// reference: https://stackoverflow.com/q/13501288
	sleepTime := 1000 * time.Millisecond // 1 second
	stunKeepAliveTimeout := 30           // in seconds
	counter := 0
	for {
		time.Sleep(sleepTime)
		switch sc.State {
		case StunStateRegistered:
			if counter++; counter > stunKeepAliveTimeout {
				deadline := time.Now().Add(stunReplyTimeout)
				handler := stun.HandlerFunc(func(e stun.Event) {
					if e.Error != nil {
						log.Println("Failed sent keep-alive packet to STUN server:", e.Error)
					} else if e.Message == nil || validateMessage(e.Message, &stun.BindingSuccess) != nil {
						log.Println("Failed sent keep-alive packet to STUN server: invalid message")
					}
				})
				if err := sc.client.Start(sc.bindMessage(), deadline, handler); err != nil {
					log.Printf("Binding failed: %v", err)
					sc.fsm <- StunTransitionBindError
				}
				counter = 0
			}
		default:
			counter = 0
		}
	}
}

func (sc *StunClient) transition(label int) {
	switch label {
	case StunTransitionBinding:
		sc.transitionBinding()
	case StunTransitionBindSuccess:
		sc.transitionBindSuccess()
	case StunTransitionBindError:
		sc.transitionBindError()
	case StunTransitionReset:
		sc.transitionReset()
	case StunTransitionStop:
		sc.transitionStop()
	case StunTransitionPeer:
	case StunTransitionNoPeer:
	default:
		log.Printf("ignored state:%d transition:%d", sc.State, label)
	}
}

func (sc *StunClient) setState(next StunState) {
	log.Println("Moved from", sc.State, "to", next)
	sc.State = next
}

func (sc *StunClient) transitionBinding() {
	switch sc.State {
	case StunStateStopped, StunStateRegistering:
		sc.setState(StunStateRegistering)
		deadline := time.Now().Add(stunReplyTimeout)
		handler := stun.HandlerFunc(func(e stun.Event) {
			if e.Error == stun.ErrTransactionTimeOut {
				sc.fsm <- StunTransitionBindError
			} else if e.Error != nil {
				log.Println("Got error", e.Error)
			} else if e.Message != nil {
				if err := validateMessage(e.Message, &stun.BindingSuccess); err != nil {
					log.Println("Invalid response message:", err)
					sc.fsm <- StunTransitionBindError
				} else {
					var xorAddr stun.XORMappedAddress
					if err = xorAddr.GetFrom(e.Message); err != nil {
						log.Println("Failed getting mapped address:", err)
					} else {
						log.Println("Mapped address", xorAddr)
					}
					sc.fsm <- StunTransitionBindSuccess
				}
			}
		})
		if err := sc.client.Start(sc.bindMessage(), deadline, handler); err != nil {
			log.Printf("Binding failed: %v", err)
			sc.fsm <- StunTransitionBindError
		}
	default:
		log.Println("Cannot do Binding transition at state", sc.State)
		return
	}
}

func (sc *StunClient) transitionStop() {
	switch sc.State {
	case StunStateStopped:
	case StunStateRegistering, StunStateRegistered, StunStateConnected:
		sc.setState(StunStateStopped)
	default:
		log.Println("Cannot do Stop transition at state", sc.State)
	}
}

func (sc *StunClient) transitionBindSuccess() {
	if sc.State == StunStateRegistering {
		sc.setState(StunStateRegistered)
	} else {
		log.Println("Cannot do BindSuccess transition at state", sc.State)
	}
}

func (sc *StunClient) transitionBindError() {
	if sc.State == StunStateRegistering {
		sc.setState(StunStateRegistrationFailed)
		sc.fsm <- StunTransitionReset
	} else {
		log.Println("Cannot do BindError transition at state", sc.State)
	}
}

func (sc *StunClient) transitionReset() {
	if sc.State == StunStateRegistrationFailed {
		// TODO: do necessary clean up here
		sc.setState(StunStateStopped)
	} else {
		log.Println("Cannot do Reset transition at state", sc.State)
	}
}

func (sc *StunClient) Stop() error {
	sc.fsm <- StunTransitionStop
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
