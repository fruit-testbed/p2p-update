package main

import (
	"time"

	"github.com/gortc/stun"
	"github.com/pkg/errors"
)

type StunClient struct {
	Id string
}

func NewStunClient() (*StunClient, error) {
	var id string
	var err error
	if id, err = localId(); err != nil {
		return nil, errors.Wrap(err, "Cannot get local id")
	}
	return &StunClient {
		Id: id,
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
		stun.NewUsername(sc.Id),
		stun.NewShortTermIntegrity(stunPassword),
		stun.Fingerprint,
	)
	deadline := time.Now().Add(time.Second * 5)
	if err := c.Do(m, deadline, f); err != nil {
		return errors.Wrap(err, "Failed to dial the server")
	}
	return nil
}
