package main

import (
  "fmt"

  "github.com/gortc/stun"
  "github.com/pkg/errors"
)

var (
  stunRealm = "fruit-testbed.org"
  stunSoftware = stun.NewSoftware("fruit/p2psecureupdate")
  stunPassword = "123"
  errNonSTUNMessage = errors.New("Not STUN Message")
)

func ValidMessage(m *stun.Message) (bool, error) {
  var soft stun.Software
  var err error

  if err = soft.GetFrom(m); err != nil {
    return false, err
  } else if soft.String() != stunSoftware.String() {
    return false, nil
  }

  var username stun.Username
  if err = username.GetFrom(m); err != nil {
    return false, err
  }

  if err = stun.Fingerprint.Check(m); err != nil {
    return false, errors.New(fmt.Sprintf("fingerprint is incorrect: %v", err))
  }

  i := stun.NewLongTermIntegrity(username.String(), stunRealm, stunPassword)
  if err = i.Check(m); err != nil {
    return false, errors.New(fmt.Sprintf("Integrity bad: %v", err))
  }

  return true, nil
}
