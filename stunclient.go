// adapted from: https://github.com/gortc/stun/blob/master/cmd/stun-client/stun-client.go

package main

import (
	"time"
	"log"

	"github.com/gortc/stun"
	"github.com/pkg/errors"
)

type StunClient struct {
	Username stun.Username
}

func NewStunClient() StunClient {
	serial, err := PiSerial()
	if err != nil {
		log.Printf("WARNING: %v", err)
	}
	return StunClient {
		Username: stun.NewUsername(serial),
	}
}

func (sc *StunClient) Dial(address string) error {
	c, err := stun.Dial("udp", address)
	if err != nil {
		return errors.Wrap(err, "Failed to dial the server")
	}
	m := stun.MustBuild(
		stun.TransactionID,
		stun.BindingRequest,
		sc.Username,
		stun.Fingerprint,
	)
	if err := c.Do(m, time.Time{}, nil); err != nil {
		return errors.Wrap(err, "Failed to dial the server")
	}
	return nil
}
