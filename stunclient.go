// adapted from: https://github.com/gortc/stun/blob/master/cmd/stun-client/stun-client.go

package main

import (
	"time"
	"os"
  "bufio"
  "strings"

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

func piSerial() (string, error) {
	file, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return "", errors.New("Cannot open /proc/cpuinfo")
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) > 10 && line[0:7] == "Serial\t" {
			return strings.TrimLeft(strings.Split(line, " ")[1], "0"), nil
		}
	}
	if err := scanner.Err(); err != nil {
		errors.Wrap(err, "Failed to read serial number")
	}
	return "", errors.New("Cannot find serial number from /proc/cpuinfo")
}

func localUsername() (string, error) {
	if serial, err := piSerial(); err == nil {
		return serial, nil
	} else {
		return os.Hostname()
	}
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
