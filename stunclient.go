// adapted from: https://github.com/gortc/stun/blob/master/cmd/stun-client/stun-client.go

package main

import (
	"time"

	"github.com/gortc/stun"
	"github.com/pkg/errors"
)

type StunClient struct {
	Username string
}

func NewStunClient() (*StunClient, error) {
	var username string
	var err error
	if username, err = localUsername(); err != nil {
		return nil, errors.Wrap(err, "Cannot get local username")
	}
	return &StunClient {
		Username: username,
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
