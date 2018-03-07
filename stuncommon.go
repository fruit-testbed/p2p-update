package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/gortc/stun"
	"github.com/pkg/errors"
	"github.com/vmihailenco/msgpack"
)

var (
	stunRealm    = "fruit-testbed.org"
	stunPassword = "123"

	maxPacketDataSize = 56 * 1024

	stunDataRequest           = stun.NewType(stun.MethodData, stun.ClassRequest)
	stunDataSuccess           = stun.NewType(stun.MethodData, stun.ClassSuccessResponse)
	stunDataError             = stun.NewType(stun.MethodData, stun.ClassErrorResponse)
	stunBindingIndication     = stun.NewType(stun.MethodBinding, stun.ClassIndication)
	stunChannelBindIndication = stun.NewType(stun.MethodChannelBind, stun.ClassIndication)

	errNonSTUNMessage = errors.New("Not STUN Message")
)

type PeerData []byte

func (pd PeerData) AddTo(m *stun.Message) error {
	m.Add(stun.AttrData, pd)
	return nil
}

type PeerID [6]byte

func (pid PeerID) String() string {
	return hex.EncodeToString(pid[:])
}

func (pid *PeerID) AddTo(m *stun.Message) error {
	m.Add(stun.AttrUsername, pid[:])
	return nil
}

func (pid *PeerID) GetFrom(m *stun.Message) error {
	var (
		buf []byte
		err error
	)

	if buf, err = m.Get(stun.AttrUsername); err != nil {
		return errors.Wrap(err, "cannot get username from the message")
	} else if len(buf) != len(pid) {
		return fmt.Errorf("length of username (%d bytes) is not 6 bytes", len(buf))
	}
	for i, b := range buf {
		pid[i] = b
	}
	return nil
}

type Peer struct {
	ID           PeerID
	InternalAddr net.UDPAddr
	ExternalAddr net.UDPAddr
}

func (p Peer) String() string {
	return fmt.Sprintf("%s[%s][%s]", p.ID.String(), p.InternalAddr.String(), p.ExternalAddr.String())
}

// SessionTable is a map whose keys are Peer IDs
// and values are pairs of [external-addr, internal-addr]
type SessionTable map[PeerID][]*net.UDPAddr

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
		err error
	)

	if t != nil && (m.Type.Method != t.Method || m.Type.Class != t.Class) {
		return fmt.Errorf("incorrect message type, expected %v but got %v",
			*t, m.Type)
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

// RaspberryPiSerial returns the board serial number retrieved from /proc/cpuinfo
func RaspberryPiSerial() (*PeerID, error) {
	file, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return nil, errors.Wrap(err, "cannot open /proc/cpuinfo")
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) > 10 && line[0:7] == "Serial\t" {
			var (
				pid    PeerID
				serial []byte
			)

			s := strings.TrimLeft(strings.Split(line, " ")[1], "0")
			if serial, err = hex.DecodeString(s); err != nil {
				return nil, errors.Wrapf(err, "failed converting %s to []byte", s)
			}
			j := len(pid) - 1
			for i := len(serial) - 1; i >= 0 && j >= 0; i-- {
				pid[j] = serial[i]
				j--
			}
			return &pid, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, errors.Wrap(err, "failed to read serial number")
	}
	return nil, errors.New("cannot find serial number from /proc/cpuinfo")
}

// ActiveMacAddress returns a MAC address of active network interface.
// Note that ActiveMacAddress iterates the interfaces returned by `net.Interfaces`
// from first to the last, and returns the first active interface.
func ActiveMacAddress() ([]byte, error) {
	var (
		ifaces []net.Interface
		err    error
	)
	if ifaces, err = net.Interfaces(); err != nil {
		return nil, err
	}
	for _, i := range ifaces {
		if i.Flags&net.FlagUp != 0 && bytes.Compare(i.HardwareAddr, nil) != 0 {
			// Don't use random as we have a real address
			return i.HardwareAddr, nil
		}
	}
	return nil, errors.New("No active ethernet available")
}

func LocalPeerID() (*PeerID, error) {
	if serial, err := RaspberryPiSerial(); err == nil {
		return serial, nil
	}

	var pid PeerID
	if mac, err := ActiveMacAddress(); err == nil && len(mac) >= 6 {
		for i, b := range mac {
			pid[i] = b
		}
		return &pid, nil
	}
	return nil, errors.New("CPU serial and active ethernet are not available")
}
