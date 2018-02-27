package main

import (
  "github.com/gortc/stun"
  "github.com/pkg/errors"
)

var (
  stunRealm = "fruit-testbed.org"
  stunSoftware = stun.NewSoftware("fruit/p2psecureupdate")
  errNonSTUNMessage = errors.New("Not STUN Message")
)
