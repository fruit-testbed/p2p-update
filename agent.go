package main

import (
	"fmt"
	"log"
	"time"
)

type Agent struct {
	Config  AgentConfig
	Overlay *OverlayConn
}

type AgentConfig struct {
	OverlayConfig OverlayConfig `json:"overlay,omitempty"`
}

func (a *Agent) Start(cfg AgentConfig) error {
	a.Config = cfg
	go a.startRestApi()
	return a.startOverlay()
}

func (a *Agent) startOverlay() error {
	var (
		msg []byte
		buf [64 * 1024]byte
		err error
	)
	if a.Overlay, err = NewOverlayConn(a.Config.OverlayConfig); err != nil {
		return err
	}
	go func() {
		for {
			if n, err := a.Overlay.Read(buf[:]); err != nil {
				log.Println("failed reading from overlay", err)
			} else {
				log.Printf("read a message from overlay: %s", string(buf[:n]))
			}
		}
	}()
	msg = []byte(fmt.Sprintf("message from %s", a.Overlay.ID))
	for {
		if _, err = a.Overlay.Write(msg); err != nil {
			log.Println("failed writing to overlay:", err)
		} else {
			log.Println("successfully wrote to overlay")
		}
		time.Sleep(time.Second)
	}
}

func (a *Agent) startRestApi() {
	// TODO: implement REST APIs over unix socket
}
