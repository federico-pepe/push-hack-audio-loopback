package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/federico-pepe/ableton-push-hack/core/audiobus"
	"github.com/federico-pepe/ableton-push-hack/core/httpx"
)

//go:embed web/index.html
var webFS embed.FS

type statusReply struct {
	State       string                `json:"state"`
	Running     bool                  `json:"running"`
	Rate        int32                 `json:"rate"`
	Period      int32                 `json:"period"`
	Channels    int32                 `json:"channels"`
	Blocks      uint64                `json:"blocks"`
	WriteErrors uint64                `json:"write_errors"`
	WorstMixUs  int64                 `json:"worst_mix_us"`
	Clients     []audiobus.ClientInfo `json:"clients"`
}

func status(srv *audiobus.Server, st *mixStats) statusReply {
	state := st.getState()
	cl := srv.Clients()
	if cl == nil {
		cl = []audiobus.ClientInfo{}
	}
	return statusReply{
		State: state, Running: state == "running",
		Rate: st.rate.Load(), Period: st.period.Load(), Channels: st.channels.Load(),
		Blocks: st.blocks.Load(), WriteErrors: st.errors.Load(),
		WorstMixUs: st.worstNs.Load() / 1000, Clients: cl,
	}
}

// runWeb serves the status page until quit closes.
func runWeb(port int, srv *audiobus.Server, st *mixStats, quit <-chan struct{}) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, status(srv, st))
	})
	mux.HandleFunc("/", httpx.ServeEmbedded(webFS, "web/index.html"))
	hs := httpx.NewServer(fmt.Sprintf(":%d", port), httpx.WithLogging(httpx.WithCORS("GET, OPTIONS", mux)))
	go func() {
		<-quit
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = hs.Shutdown(ctx)
	}()
	log.Printf("web UI on %s", hs.Addr)
	if err := hs.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("web server: %v", err)
	}
}
