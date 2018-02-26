// adapted from: https://github.com/gortc/stun/blob/master/cmd/stun-client/stun-client.go

package main

import (
	"time"
	"log"

	"github.com/gortc/stun"
	"github.com/pkg/errors"
)

type StunClient struct {
}

func (sc *StunClient) Dial(address string) error {
	c, err := stun.Dial("udp", address)
	if err != nil {
		return errors.Wrap(err, "Failed to dial the server")
	}
	var serial string
	serial, err = PiSerial()
	if err != nil {
		log.Printf("WARNING: %v", err)
	}
	m := stun.MustBuild(
		stun.TransactionID,
		stun.BindingRequest,
		stun.NewUsername(serial),
		stun.Fingerprint,
	)
	if err := c.Do(m, time.Time{}, nil); err != nil {
		return errors.Wrap(err, "Failed to dial the server")
	}
	return nil
}
