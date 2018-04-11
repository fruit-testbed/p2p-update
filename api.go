package main

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/valyala/fasthttp"
)

var (
	rUpdateURL = regexp.MustCompile("^/update/[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$")

	strPOST            = []byte("POST")
	strGET             = []byte("GET")
	strDELETE          = []byte("DELETE")
	strContentType     = []byte("Content-Type")
	strApplicationJSON = []byte("application/json")
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
	if bytes.Compare(ctx.Host(), []byte("v1")) != 0 {
		ctx.Response.SetStatusCode(400)
		return
	}
	switch {
	case bytes.Compare(ctx.Path(), []byte("/overlay/peers")) == 0:
		a.requestOverlayPeers(ctx)
	case rUpdateURL.Match(ctx.Path()):
		a.requestUpdateWithParam(ctx)
	case bytes.Compare(ctx.Path(), []byte("/update")) == 0:
		a.requestUpdate(ctx)
	default:
		ctx.Response.SetStatusCode(400)
	}
}

func (a *API) requestUpdate(ctx *fasthttp.RequestCtx) {
	switch {
	case bytes.Compare(ctx.Method(), strPOST) == 0:
		a.requestPostUpdate(ctx)
	case bytes.Compare(ctx.Method(), strGET) == 0:
		doJSONWrite(ctx, 200, a.agent.getUpdateUUIDs())
	default:
		ctx.Response.SetStatusCode(400)
	}
}

func (a *API) requestUpdateWithParam(ctx *fasthttp.RequestCtx) {
	switch {
	case bytes.Compare(ctx.Method(), strGET) == 0:
		a.requestGetUpdateWithUUID(ctx, ctx.Path()[8:])
	case bytes.Compare(ctx.Method(), strDELETE) == 0:
		a.requestDeleteUpdate(ctx, ctx.Path()[8:])
	default:
		ctx.Response.SetStatusCode(400)
	}
}

func (a *API) requestGetUpdateWithUUID(ctx *fasthttp.RequestCtx, uuid []byte) {
	update := a.agent.getUpdate(string(uuid))
	if update == nil {
		ctx.Response.SetStatusCode(404)
		return
	}
	doJSONWrite(ctx, 200, update)
}

func (a *API) requestDeleteUpdate(ctx *fasthttp.RequestCtx, uuid []byte) {
	if update := a.agent.deleteUpdate(string(uuid)); update != nil {
		update.Stop()
		if err := update.Delete(); err != nil {
			log.Printf("failed deleting update uuid:%s - %v", uuid, err)
			ctx.Response.SetStatusCode(500)
			return
		}
	}
	ctx.Response.SetStatusCode(200)
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
	switch {
	case bytes.Compare(ctx.Method(), strGET) == 0:
		ctx.Response.Header.Set("Content-Type", "application/json")
		ctx.Response.SetBody(a.agent.Overlay.peers.JSON())
	default:
		ctx.Response.SetStatusCode(400)
	}
}

func doJSONWrite(ctx *fasthttp.RequestCtx, code int, obj interface{}) {
	ctx.Response.Header.SetCanonical(strContentType, strApplicationJSON)
	ctx.Response.SetStatusCode(code)
	start := time.Now()
	if err := json.NewEncoder(ctx).Encode(obj); err != nil {
		elapsed := time.Since(start)
		log.Printf("elapsed:%v - %v", elapsed, err)
		ctx.Error(err.Error(), fasthttp.StatusInternalServerError)
	}
}