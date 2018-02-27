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
	Realm    string
	Password string
}

func NewStunClient() (*StunClient, error) {
	var serial, passwd string
	var err error
	if serial, err = PiSerial(); err != nil {
		log.Printf("WARNING: %v", err)
	}
	if passwd, err = PiPassword(); err != nil {
		return nil, err
	}
	return &StunClient {
		Username: serial,
		Realm: stunRealm,
		Password: passwd,
	}, nil
}

func (sc *StunClient) Dial(address string) error {
	c, err := stun.Dial("udp", address)
	if err != nil {
		return errors.Wrap(err, "Failed to dial the server")
	}
	m := stun.MustBuild(
		stun.TransactionID,
		stunSoftware,
		stun.NewLongTermIntegrity(sc.Username, sc.Realm, sc.Password),
		stun.NewType(stun.MethodCreatePermission, stun.ClassIndication),
		stun.Fingerprint,
	)
	if err := c.Do(m, time.Time{}, nil); err != nil {
		return errors.Wrap(err, "Failed to dial the server")
	}
	return nil
}
