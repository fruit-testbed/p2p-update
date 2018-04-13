package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/user"
	"testing"
)

func TestAgentReadTCP(t *testing.T) {
	var err error

	homeDir := "~/"
	if user, err := user.Current(); err == nil {
		homeDir = user.HomeDir
	}

	c := DefaultConfig()
	c.LogFile = ""
	c.PublicKey.Filename = fmt.Sprintf("%s/.ssh/fruit-apk-key-20170922.rsa.pub", homeDir)

	c.DataDir, err = ioutil.TempDir("", "test")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(c.DataDir)

	c.API.Address = fmt.Sprintf("%s/sock", c.DataDir)

	a, err := NewAgent(c)
	if err != nil {
		t.Errorf("failed creating agent with default config: %v", err)
	}

	if err = a.readTCP(); err != nil {
		t.Errorf("failed readTCP: %v", err)
	}
}
