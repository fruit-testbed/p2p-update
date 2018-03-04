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

	stunDataRequest       = stun.NewType(stun.MethodData, stun.ClassRequest)
	stunDataSuccess       = stun.NewType(stun.MethodData, stun.ClassSuccessResponse)
	stunDataError         = stun.NewType(stun.MethodData, stun.ClassErrorResponse)
	stunBindingIndication = stun.NewType(stun.MethodBinding, stun.ClassIndication)

	stunReplyTimeout = time.Second * 5

	errNonSTUNMessage = errors.New("Not STUN Message")
)

type Peer struct {
	ID           string
	InternalAddr net.UDPAddr
	ExternalAddr net.UDPAddr
}

func (p Peer) String() string {
	return fmt.Sprintf("%s[%s][%s]", p.ID, p.InternalAddr.String(), p.ExternalAddr.String())
}

func (p Peer) AddTo(m *stun.Message) error {
	var (
		data []byte
		err  error
	)

	if data, err = msgpack.Marshal(&p); err == nil {
		m.Add(stun.AttrData, data)
	}
	return err
}

func GetPeerFrom(m *stun.Message) (*Peer, error) {
	var (
		p    Peer
		data []byte
		err  error
	)

	if data, err = m.Get(stun.AttrData); err == nil {
		err = msgpack.Unmarshal(data, &p)
	}
	return &p, err
}

// SessionTable is a map whose keys are Peer IDs
// and values are pairs of [external-addr, internal-addr]
type SessionTable map[string][]*net.UDPAddr

func (st SessionTable) AddTo(m *stun.Message) error {
	var (
		data []byte
		err  error
	)

	if data, err = msgpack.Marshal(&st); err == nil {
		m.Add(stun.AttrData, data)
	}
	return err
}

func GetSessionTableFrom(m *stun.Message) (*SessionTable, error) {
	var (
		st   SessionTable
		data []byte
		err  error
	)

	if data, err = m.Get(stun.AttrData); err == nil {
		err = msgpack.Unmarshal(data, &st)
	}
	return &st, err
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
		return fmt.Errorf("invalid username: %v", err)
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

func localIPs() []net.IP {
	ips := make([]net.IP, 0, 5)
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			if addrs, err := iface.Addrs(); err == nil {
				for _, addr := range addrs {
					switch ip := addr.(type) {
					case *net.IPNet:
						ips = append(ips, ip.IP)
					case *net.IPAddr:
						ips = append(ips, ip.IP)
					}
				}
			}
		}
	}
	return ips
}
