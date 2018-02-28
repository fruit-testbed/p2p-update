package main

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/gortc/stun"
	"github.com/pkg/errors"
	"github.com/vmihailenco/msgpack"
)

var (
	stunRealm    = "fruit-testbed.org"
	stunSoftware = stun.NewSoftware("fruit/p2psecureupdate")
	stunPassword = "123"

	stunReplyTimeout = time.Second * 5

	errNonSTUNMessage = errors.New("Not STUN Message")
)

type Peer struct {
	ID   string
	IP   net.IP
	Port int
}

func (p Peer) String() string {
	return fmt.Sprintf("%s[%v[%d]]", p.ID, p.IP, p.Port)
}

type SessionTable map[string]Peer

func (st SessionTable) marshal() ([]byte, error) {
	return msgpack.Marshal(&st)
}

func (st SessionTable) AddTo(m *stun.Message) error {
	var (
		data []byte
		err  error
	)
	if data, err = st.marshal(); err == nil {
		m.Add(stun.AttrData, data)
	}
	return err
}

func getSessionTable(m *stun.Message) (SessionTable, error) {
	var (
		data []byte
		err  error
	)
	if data, err = m.Get(stun.AttrData); err == nil {
		return unmarshalSessionTable(data)
	}
	return nil, err
}

func unmarshalSessionTable(raw []byte) (SessionTable, error) {
	var st SessionTable
	if err := msgpack.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	return st, nil
}

func validateMessage(m *stun.Message, t *stun.MessageType) error {
	var (
		soft stun.Software
		err  error
	)

	if t != nil && (m.Type.Method != t.Method || m.Type.Class != t.Class) {
		return fmt.Errorf("incorrect message type, expected %v but got %v",
			*t, m.Type)
	}

	if err = soft.GetFrom(m); err != nil {
		return err
	} else if soft.String() != stunSoftware.String() {
		return fmt.Errorf("Invalid software: %s", soft.String())
	}

	var username stun.Username
	if err = username.GetFrom(m); err != nil {
		return err
	}

	if err = stun.Fingerprint.Check(m); err != nil {
		return fmt.Errorf("fingerprint is incorrect: %v", err)
	}

	i := stun.NewShortTermIntegrity(stunPassword)
	if err = i.Check(m); err != nil {
		return fmt.Errorf("Integrity bad: %v", err)
	}

	return nil
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

func getActiveMacAddress() (string, error) {
	var (
		ifaces []net.Interface
		err    error
	)
	if ifaces, err = net.Interfaces(); err != nil {
		return "", err
	}
	for _, i := range ifaces {
		if i.Flags&net.FlagUp != 0 && bytes.Compare(i.HardwareAddr, nil) != 0 {
			// Don't use random as we have a real address
			return i.HardwareAddr.String(), nil
		}
	}
	return "", errors.New("No active ethernet available")
}

func localID() (string, error) {
	if serial, err := piSerial(); err == nil {
		return serial, nil
	}
	if mac, err := getActiveMacAddress(); err == nil {
		return strings.Replace(mac, ":", "", -1), nil
	}
	if hostname, err := os.Hostname(); err == nil {
		return hostname, nil
	}
	return "", errors.New("CPU serial, active ethernet, and hostname are not available")
}
