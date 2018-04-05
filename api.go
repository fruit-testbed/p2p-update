package main

import (
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/valyala/fasthttp"
)

// API provides REST API implementations of the agent.
type API struct {
	agent *Agent
}

// Start starts REST API service.
func (a *API) Start() {
	err := fasthttp.ListenAndServeUNIX(a.agent.Config.API.Address, 0600, a.requestHandler)
	if err != nil {
		log.Fatalf("Error in startRestApi: %v", err)
	}
}

func (a *API) requestHandler(ctx *fasthttp.RequestCtx) {
	if string(ctx.Host()) != "v1" {
		ctx.Response.SetStatusCode(400)
		return
	}
	switch string(ctx.Path()) {
	case "/overlay/peers":
		a.requestOverlayPeers(ctx)
	case "/update":
		a.requestUpdate(ctx)
	default:
		ctx.Response.SetStatusCode(400)
	}
}

func (a *API) requestUpdate(ctx *fasthttp.RequestCtx) {
	switch string(ctx.Method()) {
	case "POST":
		a.requestPostUpdate(ctx)
	default:
		ctx.Response.SetStatusCode(400)
	}
}

func (a *API) requestPostUpdate(ctx *fasthttp.RequestCtx) {
	var (
		u   Update
		err error
	)

	if err = json.Unmarshal(ctx.PostBody(), &u); err != nil {
		log.Printf("failed to decode request update: %v", err)
		ctx.Response.SetStatusCode(400)
		return
	}
	u.agent = a.agent

	if _, err = os.Stat(u.Source); err == nil {
		dest := filepath.Join(a.agent.Config.BitTorrent.DataDir, u.Metainfo.Info.Name)
		cmd := exec.Command("cp", "-af", u.Source, dest)
		if err := cmd.Run(); err != nil {
			log.Printf("failed copying update file from '%s' to '%s': %v",
				u.Source, dest, err)
			ctx.Response.SetStatusCode(403)
			return
		}
	} else {
		log.Printf("source file '%s' does not exist", u.Source)
		ctx.Response.SetStatusCode(404)
		return
	}

	if err = u.Start(a.agent); err != nil {
		switch err {
		case errUpdateIsAlreadyExist:
			ctx.Response.SetStatusCode(208)
		case errUpdateVerificationFailed:
			ctx.Response.SetStatusCode(401)
		case errUpdateIsOlder:
			ctx.Response.SetStatusCode(406)
		default:
			ctx.Response.SetStatusCode(500)
		}
		log.Printf("failed to activating the torrent: %v", err)
	} else {
		ctx.Response.SetStatusCode(200)
	}
}

func (a *API) requestOverlayPeers(ctx *fasthttp.RequestCtx) {
	switch string(ctx.Method()) {
	case "GET":
		ctx.Response.Header.Set("Content-Type", "application/json")
		ctx.Response.SetBody(a.agent.Overlay.peers.JSON())
	default:
		ctx.Response.SetStatusCode(400)
	}
}
