// adapted from: https://github.com/gortc/stun/blob/master/cmd/stun-client/stun-client.go

package main

import (
	"time"
	"log"

	"github.com/gortc/stun"
	"github.com/pkg/errors"
)

type StunClient struct {
	Username string
}

func NewStunClient() (*StunClient, error) {
	var serial string
	var err error
	if serial, err = PiSerial(); err != nil {
		log.Printf("WARNING: %v", err)
	}
	return &StunClient {
		Username: serial,
	}, nil
}

func (sc *StunClient) Ping(address string, f func(stun.Event)) error {
	c, err := stun.Dial("udp", address)
	if err != nil {
		return errors.Wrap(err, "Failed to dial the server")
	}
	m := stun.MustBuild(
		stun.TransactionID,
		stun.NewType(stun.MethodRefresh, stun.ClassRequest),
		stunSoftware,
		stun.NewUsername(sc.Username),
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
	deadline := time.Now().Add(time.Second * 5)
	if err := c.Do(m, deadline, f); err != nil {
		return errors.Wrap(err, "Failed to dial the server")
	}
	return nil
}
