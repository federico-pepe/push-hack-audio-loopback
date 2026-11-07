package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"math"
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
	Mode        string                `json:"mode"` // "card" or "link"
	Link        linkStatus            `json:"link"`
}

// linkStatus is what the page shows when the mode is "link".
type linkStatus struct {
	Enabled bool       `json:"enabled"`
	Peers   int32      `json:"peers"` // other Link peers, as Live shows it
	Tempo   float64    `json:"tempo"`
	LeadMs  int        `json:"lead_ms"`
	Error   string     `json:"error"`
	Sinks   []sinkInfo `json:"sinks"`
}

func status(srv *audiobus.Server, st *mixStats, ctl *outputCtl) statusReply {
	state := st.getState()
	cl := srv.Clients()
	if cl == nil {
		cl = []audiobus.ClientInfo{}
	}
	errText, _ := ctl.linkErr.Load().(string)
	return statusReply{
		State: state, Running: state == "running",
		Rate: st.rate.Load(), Period: st.period.Load(), Channels: st.channels.Load(),
		Blocks: st.blocks.Load(), WriteErrors: st.errors.Load(),
		WorstMixUs: st.worstNs.Load() / 1000, Clients: cl,
		Mode: ctl.Mode(),
		Link: linkStatus{
			Enabled: ctl.linkOn.Load(), Peers: ctl.linkPeers.Load(),
			Tempo:  math.Float64frombits(ctl.tempoBits.Load()),
			LeadMs: linkLeadMs, Error: errText, Sinks: ctl.Sinks(),
		},
	}
}

// runWeb serves the status page until quit closes.
func runWeb(port int, srv *audiobus.Server, st *mixStats, ctl *outputCtl, quit <-chan struct{}) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, status(srv, st, ctl))
	})
	// POST /api/mode {"mode":"card"|"link"}. JSON only, so a form on another
	// site cannot switch the output.
	mux.HandleFunc("POST /api/mode", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "send application/json", http.StatusUnsupportedMediaType)
			return
		}
		var req struct {
			Mode string `json:"mode"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
			http.Error(w, "bad JSON", http.StatusBadRequest)
			return
		}
		if err := ctl.SetMode(req.Mode); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		httpx.JSON(w, map[string]string{"mode": ctl.Mode()})
	})
	mux.HandleFunc("/", httpx.ServeEmbedded(webFS, "web/index.html"))
	hs := httpx.NewServer(fmt.Sprintf(":%d", port), httpx.WithLogging(httpx.WithCORS("GET, POST, OPTIONS", mux)))
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
